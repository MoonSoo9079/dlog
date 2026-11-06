package server

import (
	"crypto/tls"
	"errors"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
)

const directoryTimeout = 8 * time.Second

var (
	errDirectoryConnect = errors.New("could not connect")
	errDirectoryBind    = errors.New("bind failed")
	errDirectoryBase    = errors.New("base DN was not found")
	errDirectoryGroup   = errors.New("admin group was not found")
)

// directoryAuthenticate checks the configured directory and stores the account.
// An empty password is refused before any bind. A directory that cannot be
// reached, or a service bind that fails, is ErrDirectory. A bad user password
// is ErrUnauthorized.
func (s *Server) directoryAuthenticate(username, password string) (Account, error) {
	cfg := s.store.settingsSnapshot()
	if !cfg.LDAPEnabled || password == "" || !validUsername(username) {
		return Account{}, ErrUnauthorized
	}
	name, admin, err := lookupDirectory(cfg, username, password)
	if err != nil {
		return Account{}, err
	}
	role := RoleUser
	if admin {
		role = RoleAdmin
	}
	return s.store.UpsertDirectoryUser(name, role)
}

func lookupDirectory(cfg Settings, username, password string) (string, bool, error) {
	conn, err := dialDirectory(cfg)
	if err != nil {
		slog.Warn("directory dial", "err", err)
		return "", false, ErrDirectory
	}
	defer conn.Close()
	if err := bindService(conn, cfg, false); err != nil {
		slog.Warn("directory bind", "err", err)
		return "", false, ErrDirectory
	}
	entry, err := findDirectoryUser(conn, cfg, username)
	if err != nil {
		return "", false, err
	}
	name, ok := directoryName(entry, cfg.LDAPUsernameAttr, username)
	if !ok || strings.TrimSpace(entry.DN) == "" {
		return "", false, ErrUnauthorized
	}
	admin := memberOfAdmin(entry, cfg.LDAPAdminGroup)
	if err := conn.Bind(entry.DN, password); err != nil {
		if ldap.IsErrorWithCode(err, ldap.LDAPResultInvalidCredentials) {
			return "", false, ErrUnauthorized
		}
		slog.Warn("directory user bind", "err", err)
		return "", false, ErrDirectory
	}
	if cfg.LDAPAdminGroup != "" && !admin {
		if err := bindService(conn, cfg, true); err != nil {
			slog.Warn("directory rebind", "err", err)
			return "", false, ErrDirectory
		}
		member, err := adminGroupHas(conn, cfg.LDAPAdminGroup, entry.DN, name)
		if err != nil {
			return "", false, err
		}
		admin = member
	}
	return name, admin, nil
}

// probeDirectory dials the directory, binds when a bind DN is set, and reads
// the base DN. An admin group DN is read the same way. Nothing is saved.
func probeDirectory(cfg Settings) error {
	conn, err := dialDirectory(cfg)
	if err != nil {
		slog.Warn("directory dial", "err", err)
		return errDirectoryConnect
	}
	defer conn.Close()
	if cfg.LDAPBindDN != "" {
		if cfg.LDAPBindPassword == "" {
			return errDirectoryBind
		}
		if err := conn.Bind(cfg.LDAPBindDN, cfg.LDAPBindPassword); err != nil {
			if ldap.IsErrorWithCode(err, ldap.LDAPResultInvalidCredentials) {
				return errDirectoryBind
			}
			slog.Warn("directory bind", "err", err)
			return ErrDirectory
		}
	}
	if err := directoryEntryExists(conn, cfg.LDAPBaseDN); err != nil {
		return err
	}
	if cfg.LDAPAdminGroup == "" {
		return nil
	}
	if err := directoryEntryExists(conn, cfg.LDAPAdminGroup); err != nil {
		if errors.Is(err, errDirectoryBase) {
			return errDirectoryGroup
		}
		return err
	}
	return nil
}

func directoryEntryExists(conn *ldap.Conn, dn string) error {
	req := ldap.NewSearchRequest(
		dn,
		ldap.ScopeBaseObject,
		ldap.NeverDerefAliases,
		1,
		int(directoryTimeout.Seconds()),
		false,
		"(objectClass=*)",
		[]string{"objectClass"},
		nil,
	)
	if _, err := conn.Search(req); err != nil {
		if ldap.IsErrorWithCode(err, ldap.LDAPResultNoSuchObject) {
			return errDirectoryBase
		}
		slog.Warn("directory probe", "err", err)
		return ErrDirectory
	}
	return nil
}

func dialDirectory(cfg Settings) (*ldap.Conn, error) {
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if u, err := url.Parse(cfg.LDAPURL); err == nil {
		if host := u.Hostname(); host != "" && net.ParseIP(host) == nil {
			tlsCfg.ServerName = host
		}
	}
	conn, err := ldap.DialURL(cfg.LDAPURL,
		ldap.DialWithDialer(&net.Dialer{Timeout: directoryTimeout}),
		ldap.DialWithTLSConfig(tlsCfg),
	)
	if err != nil {
		return nil, err
	}
	conn.SetTimeout(directoryTimeout)
	if cfg.LDAPStartTLS {
		if err := conn.StartTLS(tlsCfg); err != nil {
			conn.Close()
			return nil, err
		}
	}
	return conn, nil
}

// bindService binds the search account. The first search with no bind DN stays
// anonymous. again reconnects as that account after the user bind, so the user
// does not need permission to read the admin group.
func bindService(conn *ldap.Conn, cfg Settings, again bool) error {
	if cfg.LDAPBindDN == "" {
		if !again {
			return nil
		}
		return conn.UnauthenticatedBind("")
	}
	return conn.Bind(cfg.LDAPBindDN, cfg.LDAPBindPassword)
}

func findDirectoryUser(conn *ldap.Conn, cfg Settings, username string) (*ldap.Entry, error) {
	filter := strings.Replace(cfg.LDAPUserFilter, "%s", ldap.EscapeFilter(username), 1)
	req := ldap.NewSearchRequest(
		cfg.LDAPBaseDN,
		ldap.ScopeWholeSubtree,
		ldap.NeverDerefAliases,
		2,
		int(directoryTimeout.Seconds()),
		false,
		filter,
		[]string{cfg.LDAPUsernameAttr, "memberOf"},
		nil,
	)
	res, err := conn.Search(req)
	if err != nil {
		if ldap.IsErrorWithCode(err, ldap.LDAPResultSizeLimitExceeded) ||
			ldap.IsErrorWithCode(err, ldap.LDAPResultNoSuchObject) ||
			ldap.IsErrorWithCode(err, ldap.LDAPResultInvalidCredentials) {
			return nil, ErrUnauthorized
		}
		slog.Warn("directory search", "err", err)
		return nil, ErrDirectory
	}
	if len(res.Entries) != 1 {
		return nil, ErrUnauthorized
	}
	return res.Entries[0], nil
}

func directoryName(entry *ldap.Entry, attr, typed string) (string, bool) {
	for _, value := range entry.GetEqualFoldAttributeValues(attr) {
		if strings.EqualFold(value, typed) && validUsername(value) {
			return value, true
		}
	}
	return "", false
}

func memberOfAdmin(entry *ldap.Entry, adminDN string) bool {
	if adminDN == "" {
		return false
	}
	for _, value := range entry.GetEqualFoldAttributeValues("memberOf") {
		if strings.EqualFold(value, adminDN) {
			return true
		}
	}
	return false
}

func adminGroupHas(conn *ldap.Conn, adminDN, userDN, uid string) (bool, error) {
	filter := "(|(member=" + ldap.EscapeFilter(userDN) + ")(uniqueMember=" + ldap.EscapeFilter(userDN) + ")(memberUid=" + ldap.EscapeFilter(uid) + "))"
	req := ldap.NewSearchRequest(
		adminDN,
		ldap.ScopeBaseObject,
		ldap.NeverDerefAliases,
		1,
		int(directoryTimeout.Seconds()),
		false,
		filter,
		[]string{"cn"},
		nil,
	)
	res, err := conn.Search(req)
	if err != nil {
		if ldap.IsErrorWithCode(err, ldap.LDAPResultNoSuchObject) {
			return false, nil
		}
		slog.Warn("directory group", "err", err)
		return false, ErrDirectory
	}
	return len(res.Entries) > 0, nil
}
