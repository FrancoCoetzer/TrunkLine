// The command line: a second launch hands its action to the running app,
// whose page (this one too) starts it.
const { test, expect } = require("@playwright/test");
const { watchErrors, openWorkingCopy, trunkline, shot } = require("./helpers");

let noErrors;
test.beforeEach(async ({ page }) => {
  noErrors = watchErrors(page);
  await openWorkingCopy(page);
  await page.getByRole("tab", { name: "Branches" }).click(); // start away from Changes
});
test.afterEach(() => noErrors());

test("commit ticks the file and waits for a message", async ({ page }, info) => {
  await trunkline("commit", "src/motor.c");
  await expect(page.getByRole("tab", { name: "Changes" })).toHaveAttribute("aria-selected", "true");
  await expect.poll(() => page.evaluate(() => [...state.checked])).toEqual(["src/motor.c"]);
  await expect(page.locator("#diff")).toContainText('#include "encoder.h"');
  await shot(page, info, "cli-commit");
});

test("log opens the file's history", async ({ page }, info) => {
  await trunkline("log", "src/motor.c");
  await expect(page.locator("#log")).toContainText("Limit the speed");
  await expect(page.locator("#log")).not.toContainText("Project layout"); // not in this file's history
  await shot(page, info, "cli-log");
});

test("blame opens for a file", async ({ page }, info) => {
  await trunkline("blame", "src/motor.c");
  await expect(page.locator("#blameDlg")).toBeVisible();
  await expect(page.locator("#blameDlg")).toContainText("not committed");
  await shot(page, info, "cli-blame");
});

test("a path outside a working copy says so", async ({ page }) => {
  await trunkline("commit", "/");
  await expect(page.locator(".toast").last()).toContainText("is not in an SVN working copy");
});
