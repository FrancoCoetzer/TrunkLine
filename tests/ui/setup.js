// Global setup: a throwaway repository and working copy, a scratch settings
// folder, and the debug build serving the page on :34115.
//
// The repository: trunk with a branch (encoder) and a tag (v1.0), three
// commits, a file that needs a lock, and local changes (a modified file and
// a new one).
const fs = require("fs");
const os = require("os");
const path = require("path");
const http = require("http");
const { execFileSync, spawn } = require("child_process");

const root = path.resolve(__dirname, "../..");
const bin = path.join(root, "build/bin/trunkline-dev");
const state = path.join(__dirname, ".fixture.json");

const run = (cmd, args, cwd) => execFileSync(cmd, args, { cwd, stdio: "pipe" });
const svn = (args, cwd) => run("svn", ["--non-interactive", ...args], cwd);
const write = (file, text) => { fs.mkdirSync(path.dirname(file), { recursive: true }); fs.writeFileSync(file, text); };

function fixture(dir) {
  const repo = path.join(dir, "repo");
  const url = "file://" + repo;
  const wc = path.join(dir, "wc");
  run("svnadmin", ["create", repo]);
  svn(["mkdir", "--parents", "-m", "Project layout", url + "/trunk", url + "/branches", url + "/tags"]);
  svn(["checkout", "-q", url + "/trunk", wc]);
  write(path.join(wc, "src/motor.c"), "#include \"motor.h\"\n\nvoid motor_init(void) {\n  pwm_setup(20000);\n}\n");
  write(path.join(wc, "docs/readme.md"), "# Motor\n\nDriver for the main motor.\n");
  write(path.join(wc, "cad/housing.step"), "ISO-10303-21;\n");
  svn(["add", "-q", "src", "docs", "cad"], wc);
  svn(["propset", "-q", "svn:needs-lock", "*", "cad/housing.step"], wc);
  svn(["commit", "-q", "-m", "Motor driver: first version"], wc);
  svn(["copy", "-m", "v1.0", url + "/trunk", url + "/tags/v1.0"]);
  svn(["copy", "-m", "Branch for the new encoder", url + "/trunk", url + "/branches/encoder"]);
  write(path.join(wc, "src/motor.c"), "#include \"motor.h\"\n\nvoid motor_init(void) {\n  pwm_setup(25000);\n}\n");
  svn(["commit", "-q", "-m", "Limit the speed; faster PWM"], wc);
  svn(["update", "-q"], wc);
  // Local changes.
  write(path.join(wc, "src/motor.c"), "#include \"motor.h\"\n#include \"encoder.h\"\n\nvoid motor_init(void) {\n  pwm_setup(25000);\n}\n");
  write(path.join(wc, "docs/calibration.md"), "# Calibration\n");
  return wc;
}

function waitForPage(ms) {
  const until = Date.now() + ms;
  return new Promise((resolve, reject) => {
    const poll = () => http.get("http://localhost:34115/", r => { r.resume(); resolve(); })
      .on("error", () => Date.now() > until ? reject(new Error("the app didn't serve :34115")) : setTimeout(poll, 300));
    poll();
  });
}

module.exports = async () => {
  if (!fs.existsSync(bin)) throw new Error(bin + " is missing: run make ui-test (it builds it)");
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "trunkline-ui-"));
  const wc = fixture(dir);
  const cfg = path.join(dir, "cfg");
  const env = { ...process.env, XDG_CONFIG_HOME: cfg };
  if (process.env.UI_XVFB) { // under xvfb-run: GTK must use its X display
    delete env.WAYLAND_DISPLAY;
    env.GDK_BACKEND = "x11";
  }
  const assetdir = path.join(root, "frontend/dist");
  const app = spawn(bin, ["-assetdir", assetdir, "-devserver", "localhost:34115"], { env, stdio: ["ignore", "ignore", "pipe"], detached: true });
  let log = "";
  app.stderr.on("data", d => { log = (log + d).slice(-4000); });
  try {
    await waitForPage(30_000);
  } catch (e) {
    try { process.kill(-app.pid); } catch { /* gone */ }
    throw new Error(e.message + "\n" + log);
  }
  fs.writeFileSync(state, JSON.stringify({ dir, wc, cfg, bin, assetdir, pid: app.pid, env: { XDG_CONFIG_HOME: cfg, GDK_BACKEND: env.GDK_BACKEND || "" } }));
  app.unref();
};
