const path = require("path");

module.exports = {
  content: [
    path.join(__dirname, "index.html"),
    path.join(__dirname, "login.html"),
    path.join(__dirname, "account.html"),
    path.join(__dirname, "admin.html"),
    path.join(__dirname, "forbidden.html"),
    path.join(__dirname, "app.js"),
  ],
  theme: {
    extend: {
      fontFamily: {
        sans: ['"Inter"', "ui-sans-serif", "system-ui", "sans-serif"],
        mono: ["JetBrainsMono", "ui-monospace", "SFMono-Regular", "Menlo", "Consolas", "monospace"],
      },
      colors: {
        ink: {
          bg: "#121214",
          chrome: "#18181b",
          line: "#2a2a30",
          field: "#121214",
          edge: "#3a3a42",
        },
        sand: "#d9cbb3",
        clay: "#e4b2a6",
        status: {
          on: "#8eae80",
          off: "#5e5e66",
          down: "#c4897c",
        },
      },
    },
  },
  plugins: [],
};
