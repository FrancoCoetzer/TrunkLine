// Every main view, in the light and the dark theme: it renders what the
// fixture holds, logs no errors, and is attached as a screenshot to the
// report (npx playwright show-report).
const { test, expect } = require("@playwright/test");
const { watchErrors, openWorkingCopy, shot } = require("./helpers");

for (const theme of ["light", "dark"]) {
  test.describe(theme, () => {
    let noErrors;
    test.beforeEach(async ({ page }) => {
      noErrors = watchErrors(page);
      await openWorkingCopy(page, theme);
    });
    test.afterEach(() => noErrors());

    test("changes and a diff", async ({ page }, info) => {
      await expect(page.locator("#changes")).toContainText("src/motor.c");
      await expect(page.locator("#changes")).toContainText("docs/calibration.md");
      await page.locator("#changes").getByText("src/motor.c").click();
      await expect(page.locator("#diff")).toContainText('#include "encoder.h"');
      await shot(page, info, theme + "-changes");
    });

    test("history and a revision", async ({ page }, info) => {
      await page.getByRole("tab", { name: "History" }).click();
      await expect(page.locator("#log")).toContainText("Limit the speed");
      await page.locator("#log").getByText("Limit the speed").click();
      await expect(page.locator("#revDetail")).toBeVisible();
      await shot(page, info, theme + "-history");
    });

    test("branches and the revision graph", async ({ page }, info) => {
      await page.getByRole("tab", { name: "Branches" }).click();
      await expect(page.locator("#branches")).toContainText("encoder");
      await expect(page.locator("#branches")).toContainText("v1.0");
      await shot(page, info, theme + "-branches");
      await page.locator("#graphBtn").click();
      await expect(page.locator("#graphDlg")).toBeVisible();
      await shot(page, info, theme + "-graph");
    });

    test("locks", async ({ page }, info) => {
      await page.getByRole("tab", { name: "Locks" }).click();
      await expect(page.locator("#locks")).toContainText("cad/housing.step");
      await shot(page, info, theme + "-locks");
    });

    test("preferences", async ({ page }, info) => {
      await page.keyboard.press("Control+,");
      await expect(page.locator("#prefsDlg")).toBeVisible();
      await shot(page, info, theme + "-preferences");
    });
  });
}
