// TrunkLine's UI tests: see README.md in this folder.
const fs = require("fs");
const { defineConfig } = require("@playwright/test");

// The browser: $CHROME if set, else an installed Google Chrome, else the
// Chromium that `npx playwright install chromium` downloads (as CI does).
const launchOptions = {};
if (process.env.CHROME) launchOptions.executablePath = process.env.CHROME;
else if (fs.existsSync("/usr/bin/google-chrome")) launchOptions.channel = "chrome";

module.exports = defineConfig({
  testDir: ".",
  workers: 1, // one app, one fixture working copy
  timeout: 30_000,
  expect: { timeout: 8_000 },
  reporter: [["list"], ["html", { open: "never" }]],
  globalSetup: require.resolve("./setup.js"),
  globalTeardown: require.resolve("./teardown.js"),
  use: {
    baseURL: "http://localhost:34115",
    viewport: { width: 1280, height: 800 },
    launchOptions,
  },
});
