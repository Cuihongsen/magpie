const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");
const { test } = require("node:test");

const source = fs.readFileSync(path.join(__dirname, "../assets/app.js"), "utf8");
const translations = fs.readFileSync(path.join(__dirname, "../assets/i18n.js"), "utf8");
function load(context, name) {
  const start = source.indexOf(`function ${name}(`);
  assert(start >= 0, `missing ${name}`);
  vm.runInContext(source.slice(start, source.indexOf("\n}", start) + 2), context);
}

class Element {
  constructor(tag) {
    this.tagName = tag;
    this.children = [];
    this.attributes = {};
    this.validity = { badInput: false };
    this.classList = {
      add: (...names) => { this.className = [this.className, ...names].filter(Boolean).join(" "); },
      contains: (name) => (this.className || "").split(" ").includes(name),
    };
  }
  append(...children) {
    for (const child of children) { child.parent = this; this.children.push(child); }
  }
  setAttribute(name, value) { this.attributes[name] = value; }
  getAttribute(name) { return this.attributes[name] ?? null; }
  hasAttribute(name) { return name in this.attributes; }
  matches(selector) { return selector.split(", ").includes(this.tagName); }
  querySelectorAll(selector) {
    return this.children.flatMap((child) => [...(child.matches(selector) ? [child] : []), ...child.querySelectorAll(selector)]);
  }
  closest(tag) { return this.tagName === tag ? this : this.parent?.closest(tag); }
}

function editor(lang = "en") {
  const context = vm.createContext({
    Node: Element, document: { createElement: (tag) => new Element(tag) }, fieldIDs: 0,
    pickedOf: () => [], headerRows: () => [], extraBodyText: () => "", contextsText: () => "",
    proxyDraft: () => ({}), concurrencyDraft: () => ({}), draft: {},
  });
  vm.runInContext(translations, context);
  context.t = (text) => lang === "en" ? text : vm.runInContext("I18N", context)[lang][text] || assert.fail(`${lang}: missing ${text}`);
  for (const name of ["el", "field", "input", "tick", "baseAPIOf", "concurrencyDraft", "draftOf"]) load(context, name);
  return context;
}

test("opening legacy and zero-count settings shows off/50 and preserves explicit choices", () => {
  const context = editor();
  for (const [settings, on, count] of [
    [{}, false, 50],
    [{ crazyRetry: true, crazyRetryCount: 0 }, true, 50],
    [{ crazyRetry: true, crazyRetryCount: 20 }, true, 20],
    [{ crazyRetry: true, crazyRetryCount: 7 }, true, 7],
    [{ crazyRetry: false, crazyRetryCount: 100 }, false, 100],
  ]) {
    const draft = context.draftOf({ id: "relay", ...settings });
    assert.equal(draft.crazyRetry, on);
    assert.equal(Number(draft.crazyRetryCount), count);
    assert.equal(Number(draft.errorCooldownSeconds), 0);
  }
});

test("error recovery cooldown submits 0 or 1–3600 seconds and preserves saved values", async () => {
  for (const value of ["", "0", "1", "15", "3600", "-1", "3601", "1.5", "invalid"]) {
    const context = saveContext({ errorCooldownSeconds: 15 });
    assert.equal(Number(context.draft.errorCooldownSeconds), 15);
    context.draft.errorCooldownSeconds = value;
    await context.save();
    if (["", "0", "1", "15", "3600"].includes(value)) {
      assert.equal(context.posted.errorCooldownSeconds, Number(value));
      assert.equal(Number(context.draftOf(context.posted).errorCooldownSeconds), Number(value));
    } else {
      assert.equal(context.posted, undefined);
      assert(context.error);
    }
  }
});

test("a configured error cooldown opens Advanced while zero keeps it folded", () => {
  const context = editor();
  load(context, "advancedSet");
  assert.equal(context.advancedSet(context.draftOf({ errorCooldownSeconds: 0 }), {}), false);
  assert.equal(context.advancedSet(context.draftOf({ errorCooldownSeconds: 15 }), {}), true);
});

for (const lang of ["en", "zh", "zh-TW", "ja", "de"]) {
  test(`${lang}: the native tick and count retain edits when disabled or redrawn`, () => {
    const context = editor(lang);
    load(context, "crazyRetryField");
    const help = context.crazyRetryField().flatMap((node) => node.querySelectorAll("div"))
      .filter((node) => node.classList.contains("hint")).map((node) => node.textContent);
    assert.match(help[0], /100ms/);
    assert.match(help[0], /Retry-After/);
    assert.match(help[0], /50/);
    assert.match(help[1], /4xx\/5xx/);
    const controls = () => context.crazyRetryField().flatMap((node) => node.querySelectorAll("input"));
    let [toggle, count] = controls();
    assert.equal(toggle.type, "checkbox");
    assert.equal(toggle.checked, false);
    assert.equal(Number(count.value), 50);
    assert.equal(count.type, "number");
    assert.equal(count.min, "0");
    assert.equal(count.max, "100");
    assert.equal(count.step, "1");
    assert.equal(count.disabled, true);
    assert.equal(count.getAttribute("aria-label"), context.t("Retry count"));
    toggle.checked = true;
    toggle.onchange();
    assert.equal(count.disabled, false);
    count.value = "7";
    count.oninput();
    toggle.checked = false;
    toggle.onchange();
    assert.equal(count.disabled, true);
    [toggle, count] = controls();
    assert.equal(toggle.checked, false);
    assert.equal(Number(count.value), 7);
  });
}

function saveContext(settings) {
  const context = editor();
  context.draft = context.draftOf({ id: "relay", name: "Relay", chat: "http://127.0.0.1:1/v1", ...settings });
  Object.assign(context, {
    p: { id: "relay" }, pr: null, custom: true, isNew: false, decides: false,
    ed: { querySelector: () => ({ focus() {} }) }, saveBtn: {}, NO_SCROLL: {},
    apiField: { openai: "chat" }, WORKSPACE: "{workspace}", slug: (value) => value,
    chosenIds: () => [], headersOf: () => ({}), extraBodyOf: () => ({ value: {} }),
    replyHeadersOf: () => ({ value: [] }), noneChosen: () => false, bestChosen: () => false,
    modelPrefsOfDraft: () => ({}), routingOfDraft: () => ({}), searchable: () => false,
    twoKeyHeaders: () => false, unmaskable: () => true, tlsTakes: () => false,
    decideOnly: () => false, carriesCompaction: () => false, uaPassable: () => true,
    parseContexts: () => ({ map: {} }), proxyOfDraft: () => "", perKey: false,
    concurrencyOfDraft: () => null, queueOfDraft: () => ({}), priceRateOfDraft: () => 0,
    self: {}, team: false, access: false, vertex: false, endpoint: false, address: false,
    priceTypedError: () => false, accountModelsDraft: () => ({}), saving() {},
    saveAccountModelsDraft: async () => {}, shown: (node) => node,
    editorError: (message) => { context.error = message; },
    providerAction: (action, body) => { context.posted = JSON.parse(JSON.stringify(body)); },
  });
  if (source.includes("function crazyRetryOfDraft(")) load(context, "crazyRetryOfDraft");
  const start = source.indexOf("  const save = () => {", source.indexOf("function drawEditor("));
  const end = source.indexOf("\n  saveBtn.onclick = save;", start);
  assert(start >= 0 && end > start);
  vm.runInContext(source.slice(start, end) + "\nthis.save = save;", context);
  return context;
}

test("Save submits booleans and integer counts and reopening echoes the submitted settings", async () => {
  for (const [settings, on, count] of [
    [{}, false, 50],
    [{ crazyRetry: true, crazyRetryCount: 7 }, true, 7],
    [{ crazyRetry: false, crazyRetryCount: 7 }, false, 7],
  ]) {
    const context = saveContext(settings);
    await context.save();
    assert.equal(context.posted.crazyRetry, on);
    assert.equal(context.posted.crazyRetryCount, count);
    const reopened = context.draftOf(context.posted);
    assert.equal(reopened.crazyRetry, on);
    assert.equal(Number(reopened.crazyRetryCount), count);
  }
});

test("Save accepts zero or blank as the default and rejects invalid counts before posting", async () => {
  for (const value of ["", "0", "1", "100", "-1", "1.5", "101", "oops"]) {
    const context = saveContext({ crazyRetry: true });
    context.draft.crazyRetryCount = value;
    await context.save();
    if (["", "0", "1", "100"].includes(value)) {
      assert.equal(context.posted.crazyRetryCount, Number(value));
    } else {
      assert.equal(context.posted, undefined, `${value} must not be saved`);
      assert(context.error, `${value} needs an inline error`);
    }
  }
});
