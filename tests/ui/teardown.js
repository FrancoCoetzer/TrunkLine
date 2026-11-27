// Global teardown: stop the app and remove the fixture.
const fs = require("fs");
const path = require("path");

module.exports = async () => {
  const file = path.join(__dirname, ".fixture.json");
  if (!fs.existsSync(file)) return;
  const fx = JSON.parse(fs.readFileSync(file, "utf8"));
  try { process.kill(-fx.pid); } catch { /* already gone */ }
  fs.rmSync(fx.dir, { recursive: true, force: true });
  fs.rmSync(file);
};
