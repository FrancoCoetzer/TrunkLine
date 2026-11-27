# UI tests

Playwright drives the page that the debug build serves on
http://localhost:34115 (with the Go methods), against a throwaway repository.

```sh
make ui-test                 # from the repository root: builds, installs, runs
cd tests/ui && npx playwright show-report   # the screenshots of every view
```

- `setup.js` builds the fixture (trunk, a branch, a tag, a file that needs a
  lock, local changes), points `XDG_CONFIG_HOME` at a scratch folder and
  starts `build/bin/trunkline-dev`. `teardown.js` stops it and removes it all.
- `views.spec.js`: each main view in the light and dark theme, attached as a
  screenshot; `cli.spec.js`: the command line against the running app.
- Every test fails on a console error of ours (`helpers.js` lists the known
  ones that aren't).
- The browser is `$CHROME`, else Google Chrome if it's installed, else
  Playwright's Chromium (`npx playwright install chromium`).
- With `xvfb-run` installed (CI has it), the app's own window opens on a
  virtual display instead of your desktop.
- Port 34115 must be free: stop a running `make dev` first.
