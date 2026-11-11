const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const vm = require("node:vm");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");

const assets = path.resolve(__dirname, "../assets");
const words = {};

function listing(kind) {
  const account = kind === "builtin" ? "codex" : "relay-plugin";
  const provider = {
    id: kind === "key" ? "relay" : account, name: "Retry fixture", icon: "generic",
    chat: kind === "key" ? "https://relay.example/v1" : kind === "plugin" ? "plugin://relay" : "",
    responses: "", anthropic: "", catalog: "", agents: [], headers: {}, fallback: [], keyList: [],
    models: [{ id: "gpt-6", name: "GPT", on: true }], key: { set: true, masked: "test-key" },
  };
  if (kind !== "key") provider.account = {
    agent: account, agentName: "Retry fixture", user: "test@example.invalid", plan: "Pro",
    logins: [{ user: "test@example.invalid", plan: "Pro", active: true, on: true }],
  };
  return {
    providers: [provider], presets: [], excluded: [], gateway: { running: true, window: true },
    plugins: kind === "plugin" ? [{
      id: account, pid: account, name: "Retry fixture", icon: "generic",
      spec: "npm:fixture-auth", methods: [],
    }] : [],
  };
}

async function open(browser, kind, lang, cleanup) {
  const page = await browser.newPage({ viewport: { width: 1000, height: 900 }, reducedMotion: "reduce" });
  cleanup.after(() => page.close());
  page.setDefaultTimeout(3000);
  page.setDefaultNavigationTimeout(60000);
  const state = listing(kind), posts = [], errors = [];
  page.on("pageerror", (error) => errors.push(error.message));
  await page.route("**/*", async (route) => {
    const url = new URL(route.request().url());
    const json = (data) => route.fulfill({ json: data });
    if (url.pathname === "/boot.js") return route.fulfill({ contentType: "text/javascript", body: `window.bootPrefs={lang:${JSON.stringify(lang)},theme:"light",web:true};` });
    if (url.pathname === "/wails/runtime.js") return route.fulfill({ contentType: "text/javascript", body: "export const Window={};" });
    if (url.pathname === "/api/state") return json({ agents: [], profiles: [], settings: { lang, theme: "light" } });
    if (url.pathname === "/api/providers") return json(state);
    if (url.pathname === "/api/groups") return json({ groups: [], models: [] });
    if (url.pathname === "/api/provider/save") {
      const body = route.request().postDataJSON();
      posts.push(body);
      Object.assign(state.providers[0], { crazyRetry: body.crazyRetry, crazyRetryCount: body.crazyRetryCount || 50, errorCooldownSeconds: body.errorCooldownSeconds });
      return json(state);
    }
    if (url.pathname.startsWith("/api/")) return json({});
    const file = path.join(assets, url.pathname === "/" ? "index.html" : url.pathname);
    try {
      return route.fulfill({ body: await fs.readFile(file), contentType: {
        ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml",
      }[path.extname(file)] });
    } catch { return route.fulfill({ status: 404, body: "" }); }
  });
  await page.goto("http://magpie.test/?view=providers");
  const edit = async () => {
    await page.locator(`.row.provider[data-id="${state.providers[0].id}"]`).click();
    const editor = page.locator(".editor");
    await editor.waitFor();
    if (!(await editor.locator(".advanced").evaluate((element) => element.open))) await editor.locator(".advanced > summary").click();
    return editor;
  };
  const translate = (text) => lang === "en" ? text : words.I18N[lang][text] || assert.fail(`${lang}: ${text}`);
  const controls = (editor) => ({
    toggle: editor.locator("label.tick").filter({ hasText: translate("Crazy retry") }).locator("input"),
    count: editor.locator(".crazy-retry-count"),
    save: editor.getByRole("button", { name: translate("Save"), exact: true }),
  });
  return { page, posts, errors, edit, controls };
}

for (const engine of process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"]) {
  for (const lang of ["en", "zh", "zh-TW", "ja", "de"]) {
    for (const kind of ["builtin", "plugin", "key"]) {
      test(`${engine} ${lang} ${kind}: full editor saves retry settings and refuses badInput`, async (cleanup) => {
        const browser = await (engine === "webkit" ? webkit.launch({ headless: true }) : chromium.launch({
          headless: true, executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE,
        }));
        cleanup.after(() => browser.close());
        if (!words.I18N) vm.runInNewContext(await fs.readFile(path.join(assets, "i18n.js"), "utf8") + "\nthis.I18N=I18N;", words);
        const { page, posts, errors, edit, controls } = await open(browser, kind, lang, cleanup);
        let editor = await edit(), { toggle, count, save } = controls(editor);
        const cooldown = editor.locator(".error-cooldown");
        assert.equal(await cooldown.inputValue(), "0");
        assert.equal(await cooldown.isDisabled(), false);
        assert.equal(await cooldown.getAttribute("min"), "0");
        assert.equal(await cooldown.getAttribute("max"), "3600");
        await cooldown.fill("15");
        assert.equal(await count.inputValue(), "50");
        assert.equal(await toggle.isChecked(), false);
        assert.equal(await count.isDisabled(), true);
        await toggle.check();
        await count.fill("7");
        await save.click();
        await editor.waitFor({ state: "detached" });
        assert.equal(posts[0].crazyRetry, true);
        assert.equal(posts[0].crazyRetryCount, 7);
        assert.equal(posts[0].errorCooldownSeconds, 15);
        editor = await edit();
        ({ toggle, count, save } = controls(editor));
        assert.equal(await toggle.isChecked(), true);
        assert.equal(await count.inputValue(), "7");
        assert.equal(await cooldown.inputValue(), "15");
        await toggle.uncheck();
        await save.click();
        await editor.waitFor({ state: "detached" });
        assert.equal(posts[1].crazyRetry, false);
        assert.equal(posts[1].crazyRetryCount, 7);
        editor = await edit();
        ({ toggle, count, save } = controls(editor));
        assert.equal(await count.inputValue(), "7");
        assert.equal(await count.isDisabled(), true);
        await toggle.check();
        await count.fill("");
        await count.pressSequentially("1e");
        assert.deepEqual(await count.evaluate((element) => ({ value: element.value, bad: element.validity.badInput })), { value: "", bad: true });
        await save.click();
        await page.waitForTimeout(100);
        assert.equal(posts.length, 2, "badInput must not post a save with the default count");
        assert.equal(await count.evaluate((element) => document.activeElement === element), true);
        assert.match(await editor.locator(".editor-error").innerText(), /\S/);
        await count.fill("");
        await save.click();
        await editor.waitFor({ state: "detached" });
        assert.equal(posts[2].crazyRetryCount, 0, "a genuinely empty field uses the default");
        editor = await edit();
        ({ count, save } = controls(editor));
        assert.equal(await count.inputValue(), "50");
        for (const value of ["1", "100", "0"]) {
          await count.fill(value);
          await save.click();
          await editor.waitFor({ state: "detached" });
          assert.equal(posts.at(-1).crazyRetryCount, Number(value));
          editor = await edit();
          ({ count, save } = controls(editor));
          assert.equal(await count.inputValue(), value === "0" ? "50" : value);
        }
        if (lang === "en") {
          const saved = posts.length;
          for (const invalid of ["-1", "1.5", "3601"]) {
            await cooldown.fill(invalid);
            await save.click();
            assert.equal(posts.length, saved);
            assert.equal(await cooldown.evaluate((element) => document.activeElement === element), true);
            assert.match(await editor.locator(".editor-error").innerText(), /\S/);
          }
          await cooldown.fill("");
          await cooldown.pressSequentially("1e");
          assert.deepEqual(await cooldown.evaluate((element) => ({ value: element.value, bad: element.validity.badInput })), { value: "", bad: true });
          await save.click();
          assert.equal(posts.length, saved);
          for (const value of ["", "1", "3600", "0"]) {
            await cooldown.fill(value);
            await save.click();
            await editor.waitFor({ state: "detached" });
            assert.equal(posts.at(-1).errorCooldownSeconds, Number(value));
            editor = await edit();
            ({ save } = controls(editor));
            assert.equal(await cooldown.inputValue(), String(Number(value)));
          }
        }
        assert.deepEqual(errors, []);
      });
    }
  }
}
