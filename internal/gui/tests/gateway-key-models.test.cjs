// The models a gateway key may use (#882): picked in the app's own menu
// on the key's row, sent when the menu closes, "All models" taking the
// restriction off.
const assert = require("node:assert/strict");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");
const { fixture } = require("./fixtures/caller-keys.cjs");

for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  for (const lang of ["en", "zh"]) {
    test(`${engine} ${lang}: a gateway key is held to the models picked for it`, async (t) => {
      const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
      t.after(() => browser.close());
      const page = await (await browser.newContext({ viewport: { width: 1000, height: 420 }, reducedMotion: "reduce" })).newPage();
      page.setDefaultTimeout(6000);
      const events = [], errors = [];
      page.on("pageerror", (e) => errors.push(e.message));
      await page.route("**/*", fixture(lang, "light", events, { lan: true, limits: { server: { models: ["openai/*", "relay/m"] } } }));
      const zh = lang === "zh";
      const w = zh
        ? { label: "此密钥可用的模型", all: "全部模型", two: "2 个模型", relay: "Relay 的全部模型", only: /仅限这些模型/ }
        : { label: "Models this key may use", all: "All models", two: "2 models", relay: "Every Relay model", only: /Only these models/ };
      await page.goto("http://magpie.test/?view=gateway");
      await page.locator("#gatewayKeys .acc[data-key]").last().waitFor();
      const row = (id) => page.locator(`#gatewayKeys .acc[data-key="${id}"]`);
      const badge = (id) => row(id).getByRole("button", { name: w.label, exact: true });
      // a held key says how many it may use, always; a free one "All models"
      assert.equal((await badge("server").textContent()).trim(), w.two);
      assert.match(await badge("server").getAttribute("title"), w.only);
      assert.equal(await badge("server").evaluate((b) => b.classList.contains("set")), true);
      assert.equal((await badge("work").textContent()).trim(), w.all);
      // nothing drawn with a coloured left border
      const lefts = await page.locator("#gatewayKeys .key-models").evaluateAll((els) => els.map((e) => getComputedStyle(e).borderLeftStyle));
      assert(lefts.every((s) => s === "none"), "a badge has a left border");
      const view = page.locator("#view-gateway");
      const scrolled = async () => [await view.evaluate((v) => v.scrollTop), await page.evaluate(() => window.scrollY)];
      await page.mouse.move(500, 300);
      await page.mouse.wheel(0, 120);
      // WebKit animates the wheel; start measuring clicks after it settles.
      await page.waitForFunction(() => {
        const top = document.querySelector("#view-gateway").scrollTop, now = performance.now();
        const last = window.keyScrollSample ||= { top: -1, at: now };
        if (last.top !== top) Object.assign(last, { top, at: now });
        return top > 0 && now - last.at > 300;
      });
      const at = await scrolled();
      // the app's menu, not a native select
      await row("work").hover();
      await badge("work").click();
      const menu = page.locator(".proto-menu");
      await menu.waitFor();
      assert.equal(await page.locator("select").count(), 0, "no native select");
      assert.equal((await menu.locator(".pm-head").textContent()).trim(), w.label);
      assert.deepEqual(await scrolled(), at, "opening the menu moved the page");
      assert.equal(await menu.locator(".pm-item.on").count(), 1, "All models is ticked for a free key");
      // A provider heading folds without selecting or saving anything.
      const relayGroup = menu.locator(".pm-group").filter({ has: page.locator(".gn", { hasText: /^Relay$/ }) });
      const fold = relayGroup.locator(".am-fold");
      await fold.click();
      assert.equal(await fold.getAttribute("aria-expanded"), "false");
      assert.equal(await relayGroup.locator(".pm-item").first().isVisible(), false);
      assert.equal(events.filter((e) => e.action === "models-key").length, 0);
      // Search opens matching groups, including their ids and accounts;
      // clearing it restores the reader's folded state.
      const search = menu.locator("input");
      await search.fill("m-mini");
      assert.equal(await menu.locator(".pm-item:visible").count(), 2);
      assert.equal(await relayGroup.locator(".pm-item", { hasText: "Model mini" }).isVisible(), true);
      await search.fill("");
      assert.equal(await fold.getAttribute("aria-expanded"), "false");
      await fold.click();
      // a short window: the menu stays in it, scrolling, rather than running off it
      // (reduced motion still runs a .01ms transition, so it is waited for)
      await page.waitForFunction(() => {
        const r = document.querySelector(".proto-menu").getBoundingClientRect();
        return r.top >= 0 && r.bottom <= innerHeight;
      }, null, { timeout: 2000 }).catch(async () => assert.fail(`the menu runs off the window: ${JSON.stringify(await menu.boundingBox())}`));
      // picks stay in the menu, sent only when it closes
      await menu.locator(".pm-item", { hasText: w.relay }).click();
      await menu.locator(".pm-item", { hasText: "GPT-5" }).click();
      assert.equal(events.filter((e) => e.action === "models-key").length, 0, "a pick was sent before the menu closed");
      assert.equal(await menu.isVisible(), true, "a pick closed the menu");
      await page.keyboard.press("Escape");
      await menu.waitFor({ state: "detached" });
      assert.deepEqual(events.filter((e) => e.action === "models-key").map((e) => e.body), [{ key: "work", models: ["relay/*", "openai/gpt-5"] }]);
      await page.waitForFunction(() => document.querySelector('#gatewayKeys .acc[data-key="work"] .key-models.set'));
      assert.equal((await badge("work").textContent()).trim(), w.two);
      assert.deepEqual(await scrolled(), at, "a pick moved the page");
      // All models takes it off
      await badge("server").click();
      await menu.waitFor();
      assert.equal(await menu.locator(".pm-item.on").count(), 2);
      await menu.locator(".pm-item", { hasText: w.all }).first().click();
      await menu.waitFor({ state: "detached" });
      assert.deepEqual(events.filter((e) => e.action === "models-key").at(-1).body, { key: "server", models: [] });
      await page.waitForFunction(() => !document.querySelector('#gatewayKeys .acc[data-key="server"] .key-models.set'));
      assert.deepEqual(await scrolled(), at, "no click moved the page");
      // narrow: the row fits
      await page.setViewportSize({ width: 560, height: 740 });
      await page.waitForFunction(() => document.documentElement.scrollWidth <= window.innerWidth);
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth), false);
      assert.deepEqual(errors, []);
    });
  }
}

for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  for (const lang of ["en", "zh", "zh-TW", "ja", "de"]) {
    for (const width of [1100, 420]) {
      test(`${engine} ${lang} ${width}: grouped key search preserves hidden permissions`, async (t) => {
        const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
        t.after(() => browser.close());
        const page = await browser.newPage({ viewport: { width, height: 800 }, reducedMotion: "reduce" });
        const events = [], errors = [];
        page.on("pageerror", (e) => errors.push(e.message));
        const base = fixture(lang, "dark", events, { lan: true, limits: { work: { models: ["old/*", "relay/m"] } } });
        await page.route("**/*", async (route) => {
          if (new URL(route.request().url()).pathname !== "/api/caller-keys/models") return base(route);
          return route.fulfill({ json: {
            models: [
              { id: "group/fast", name: "Fast", group: true },
              ...Array.from({ length: 12 }, (_, i) => ({ id: `relay/${i ? "m-" + i : "m"}`, name: `Model ${i}`, provider: "relay", providerName: "Relay" })),
              { id: "other/m", name: "Other model", provider: "other", providerName: "Other" },
            ],
            accounts: [{ id: "relay/team", name: "Team account", provider: "relay", providerName: "Relay" }],
          } });
        });
        await page.goto("http://magpie.test/?view=gateway");
        await page.locator('#gatewayKeys [data-key="work"] .key-models').click();
        const menu = page.locator(".proto-menu");
        await menu.waitFor();
        const relay = menu.locator(".pm-group").filter({ has: page.locator(".gn", { hasText: /^Relay$/ }) });
        assert.equal(await relay.locator(".am-fold").getAttribute("aria-expanded"), "false", "long providers begin folded");
        assert.equal(await relay.locator(".pm-item").first().isVisible(), false);
        const pick = relay.locator(".pm-group-pick");
        assert.equal(await pick.getAttribute("aria-checked"), "mixed");
        const search = menu.locator("input");
        await search.fill("m-11");
        await menu.locator(".pm-item", { hasText: "Model 11" }).click();
        await search.fill("Team account");
        await menu.locator(".pm-item", { hasText: "Team account" }).click();
        await search.fill("not-a-model");
        assert.equal(await menu.locator(".pm-group:visible").count(), 0);
        assert.equal(events.filter((e) => e.action === "models-key").length, 0);
        await search.fill("");
        assert.equal(await relay.locator(".am-fold").getAttribute("aria-expanded"), "false");
        await relay.locator(".am-fold").focus();
        await page.keyboard.press("ArrowDown");
        assert.equal(await page.evaluate(() => document.activeElement.matches(".pm-group-pick")), true, "keyboard reaches the next group without entering folded rows");
        await pick.click();
        assert.equal(await pick.getAttribute("aria-checked"), "true");
        await pick.click();
        assert.equal(await pick.getAttribute("aria-checked"), "false");
        assert.equal(await relay.locator(".pm-item.on").count(), 1, "clearing all models preserves the selected account");
        assert.match(await relay.locator(".pm-item.on").textContent(), /Team account/);
        await pick.focus();
        await page.keyboard.press("Space");
        assert.equal(await pick.getAttribute("aria-checked"), "true");
        const box = await menu.boundingBox();
        assert(box.x >= 0 && box.x + box.width <= width && box.y + box.height <= 800, JSON.stringify(box));
        await page.keyboard.press("Escape");
        await page.waitForFunction(() => !document.querySelector(".proto-menu"));
        await page.waitForFunction(() => document.querySelector('#gatewayKeys [data-key="work"] .key-models').title.includes("relay/*"));
        assert.deepEqual(events.find((e) => e.action === "models-key").body, { key: "work", models: ["relay/*", "old/*"] });
        // The account write follows the model write; wait for both responses.
        await page.waitForFunction(() => document.querySelector('#gatewayKeys [data-key="work"] .key-models').title.includes("relay/team"));
        assert.deepEqual(events.find((e) => e.action === "accounts-key").body, { key: "work", accounts: ["relay/team"] });
        assert.deepEqual(errors, []);
      });
    }
  }
}
