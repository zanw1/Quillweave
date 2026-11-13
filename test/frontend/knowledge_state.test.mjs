import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import ts from "typescript";

const root = fileURLToPath(new URL("../../", import.meta.url));

// Exercise the real shared timeline rules without component stubs or temporary files.
test("retired interpretations stay out of chapter history while genuine past states remain", () => {
  const source = fs.readFileSync(path.join(root, "frontend/src/domain/memory.ts"), "utf8");
  const compiled = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText;
  const rules = {};
  new Function("exports", compiled)(rules);
  for (const [name, status, state, chapter, end, next, visible] of [
    ["retired at opening", "superseded", "superseded_by_rebuild", 1, 1400, 0, false],
    ["retired at latest chapter", "superseded", "superseded_by_rebuild", 260, 1400, 0, false],
    ["normalized retirement marker", "superseded", " SUPERSEDED_BY_REBUILD ", 1, 1400, 0, false],
    ["revoked with a later replacement", "superseded", "superseded_by_rebuild", 1, 1400, 10, false],
    ["confirmed rule mentions retirement", "confirmed", "active", 1, 1400, 0, true],
    ["bounded historical state", "superseded", "alive", 3, 9, 0, true],
    ["before linked replacement", "superseded", "alive", 3, 19, 20, true],
    ["at linked replacement", "superseded", "alive", 20, 1400, 20, false],
    ["rejected rule", "rejected", "active", 1, 1400, 0, false],
    ["unaccepted rule", "proposed", "active", 1, 1400, 0, false],
  ]) {
    const memory = { id: "old", status, state, chapter: 1, through_chapter: end, content: "mentions superseded_by_rebuild" };
    const versions = next ? [memory, { id: "new", supersedes: "old", status: "confirmed", chapter: next }] : [memory];
    assert.equal(rules.memoryVisibleAt(memory, chapter, rules.memorySuccessors(versions)), visible, name);
  }
});

/** Preserve JSX structure while keeping child rendering outside these component state tests. */
function element(type, props) {
  return { type, props: props || {} };
}

/** Give component boundary stubs stable names for accessible action assertions. */
function boundary(name) {
  return Object.assign(() => null, { displayName: name });
}

/** Compare hook dependencies without treating a fresh array wrapper as a changed dependency. */
function sameDependencies(left, right) {
  return left && right && left.length === right.length && left.every((value, index) => Object.is(value, right[index]));
}

/** Execute the current TSX with isolated hooks and transport, retaining real component event handlers. */
function mount(relativePath, exportName, initialProps, overrides = {}) {
  const slots = [];
  let cursor = 0;
  let changed = false;
  let effects = [];
  let tree;
  let props = initialProps;
  const react = {
    useState(initial) {
      const index = cursor++;
      if (!(index in slots)) slots[index] = typeof initial === "function" ? initial() : initial;
      return [slots[index], (next) => {
        const value = typeof next === "function" ? next(slots[index]) : next;
        if (!Object.is(value, slots[index])) changed = true;
        slots[index] = value;
      }];
    },
    useEffect(run, dependencies) {
      const index = cursor++;
      if (sameDependencies(slots[index]?.dependencies, dependencies)) return;
      effects.push(() => {
        slots[index]?.cleanup?.();
        slots[index] = { dependencies, cleanup: run() };
      });
    },
    useMemo(compute, dependencies) {
      const index = cursor++;
      if (!sameDependencies(slots[index]?.dependencies, dependencies))
        slots[index] = { dependencies, value: compute() };
      return slots[index].value;
    },
    lazy: () => boundary("Lazy"),
    Suspense: boundary("Suspense"),
  };
  const ui = new Proxy({}, { get: (_target, name) => boundary(String(name)) });
  const memoryRules = {
    memorySuccessors: () => new Map(),
    memoryVisibleAt: () => true,
  };
  const modules = {
    react,
    "react/jsx-runtime": { jsx: element, jsxs: element, Fragment: "Fragment" },
    "../../lib/i18n": { t: (value) => value },
    "../../lib/api": { api: async () => ({}), contentHash: async (text) => `hash:${text}`, message: String },
    "../../components/ui": ui,
    "../../components/Select": { Select: boundary("Select") },
    "@tabler/icons-react": ui,
    "../../domain/types": { entityKinds: {}, factKinds: { event: "Event" } },
    "../../domain/memory": memoryRules,
    "./setupDrafts": { publishSetupDraft() {} },
    "../studio/setupDrafts": { publishSetupDraft() {} },
    "./StoryMemory": { StoryMemory: boundary("StoryMemory") },
    ...overrides,
  };
  const source = fs.readFileSync(path.join(root, relativePath), "utf8");
  const compiled = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, jsx: ts.JsxEmit.ReactJSX, target: ts.ScriptTarget.ES2022 },
    fileName: relativePath,
  }).outputText;
  const exports = {};
  new Function("require", "exports", compiled)((name) => {
    assert.ok(name in modules, `Unexpected component dependency ${name}`);
    return modules[name];
  }, exports);
  const component = exports[exportName];
  return {
    get tree() { return tree; },
    async render(nextProps = props) {
      props = nextProps;
      for (let attempt = 0; attempt < 12; attempt++) {
        changed = false;
        cursor = 0;
        effects = [];
        tree = component(props);
        for (const run of effects) run();
        await Promise.resolve();
        await Promise.resolve();
        if (!changed) return tree;
      }
      assert.fail("Component state did not settle");
    },
    close() {
      for (const slot of slots) slot?.cleanup?.();
    },
  };
}

/** Walk rendered elements without inventing behavior for child components. */
function nodes(value) {
  if (Array.isArray(value)) return value.flatMap(nodes);
  if (!value || typeof value !== "object") return [];
  return [value, ...nodes(value.props?.children)];
}

/** Read the text supplied by this component, excluding child component implementation details. */
function text(value) {
  if (Array.isArray(value)) return value.map(text).join("");
  if (value === null || value === undefined || typeof value === "boolean") return "";
  return typeof value === "object" ? text(value.props?.children) : String(value);
}

/** Find exactly one visible action so duplicate or missing navigation fails the regression. */
function action(tree, label) {
  const matches = nodes(tree).filter((node) => typeof node.props?.onClick === "function" && text(node) === label);
  assert.equal(matches.length, 1, `Expected one action ${label}`);
  return matches[0];
}

/** Build source-linked fixtures without a browser profile or user database. */
function fixture() {
  return {
    id: "story", revision: 1, entities: [], sources: [],
    chapters: [{ id: "chapter", title: "Source", body: "CURRENT" }],
    studio: { memories: [], plans: [] }, facts: [],
  };
}

test("stale memory loses the green approval badge and links to its source", async () => {
  const project = fixture();
  project.studio.memories.push({
    id: "memory", kind: "fact", title: "Location", content: "Known location", entity: "Mara",
    status: "confirmed", state: "active", chapter: 1, through_chapter: 0, due_chapter: 0,
    source_id: "chapter", source_hash: "hash:PREVIOUS", quote: "PREVIOUS", known_by: [],
  });
  let opened = "";
  const component = mount("frontend/src/features/studio/Memories.tsx", "Memories", {
    project, onProject() {}, onWiki() {}, onChapter: (id) => { opened = id; },
  });
  try {
    await component.render();
    const badges = nodes(component.tree).filter((node) => node.type?.displayName === "Badge");
    assert.ok(badges.some((node) => text(node) === "Dependencies changed" && node.props.tone === "red"));
    assert.ok(!badges.some((node) => node.props.tone === "green"));
    action(component.tree, "Back to source").props.onClick();
    assert.equal(opened, "chapter");
    const proposed = structuredClone(project);
    proposed.studio.memories[0].status = "proposed";
    await component.render({ project: proposed, onProject() {}, onWiki() {} });
    assert.equal(action(component.tree, "Accept").props.disabled, true);
    const reviewed = structuredClone(proposed);
    reviewed.studio.memories[0].source_hash = "hash:CURRENT";
    reviewed.studio.memories[0].quote = "CURRENT";
    await component.render({ project: reviewed, onProject() {}, onWiki() {} });
    assert.equal(action(component.tree, "Accept").props.disabled, false);
    reviewed.studio.memories[0].source_hash = "";
    await component.render({ project: structuredClone(reviewed), onProject() {}, onWiki() {} });
    assert.equal(action(component.tree, "Accept").props.disabled, false);
  } finally {
    component.close();
  }
});

test("pending evidence combines planning range and cited identity filters", async () => {
  const project = fixture();
  project.studio.plans = [{ id: "first-volume", title: "First volume", level: "volume", status: "confirmed", start_chapter: 1, end_chapter: 2 }];
  project.facts = [1, 2, 3].map((chapter) => ({
    id: `fact-${chapter}`, chapter, entity: "Mara", kind: "event", status: "pending",
    claim: `CHAPTER_${chapter}_EVIDENCE`, source_id: "chapter", source_hash: "hash:CURRENT", quote: "CURRENT",
  }));
  const component = mount("frontend/src/features/wiki/Wiki.tsx", "Wiki", { project, onProject() {}, onChapter() {} });
  /** Change the visible scope control using the component's production event handler. */
  function changeField(label, value) {
    const field = nodes(component.tree).find((node) => node.props?.["aria-label"] === label);
    assert.ok(field, `Missing control ${label}`);
    field.props.onChange({ target: { value } });
  }
  try {
    await component.render();
    action(component.tree, "Story memory").props.onClick();
    await component.render();
    nodes(component.tree).find((node) => node.type?.displayName === "StoryMemory").props.onReview(["fact-1", "fact-3"]);
    await component.render();
    changeField("Planning scope", "first-volume");
    await component.render();
    assert.match(text(component.tree), /CHAPTER_1_EVIDENCE/);
    assert.doesNotMatch(text(component.tree), /CHAPTER_[23]_EVIDENCE/);
    action(component.tree, "Clear cited evidence filter").props.onClick();
    await component.render();
    assert.match(text(component.tree), /CHAPTER_2_EVIDENCE/);
    assert.doesNotMatch(text(component.tree), /CHAPTER_3_EVIDENCE/);
    changeField("Review start chapter", "3");
    changeField("Review end chapter", "1");
    await component.render();
    assert.match(text(component.tree), /Choose a valid chapter range/);
    assert.doesNotMatch(text(component.tree), /CHAPTER_1_EVIDENCE/);
    action(component.tree, "Show all pending evidence").props.onClick();
    await component.render();
    assert.match(text(component.tree), /CHAPTER_3_EVIDENCE/);
  } finally {
    component.close();
  }
});

test("reviewing compilation citations opens only its pending evidence", async () => {
  const project = fixture();
  project.facts = ["CITED_FACT", "UNRELATED_FACT"].map((claim, index) => ({
    id: `fact-${index}`, entity: "Mara", kind: "event", status: "pending", claim,
    source_id: "chapter", source_hash: "hash:CURRENT", quote: "CURRENT", chapter: 1,
  }));
  const health = { compilations: [{ current: true, source_title: "Source", record: {
    id: "compiled", status: "confirmed", source_id: "chapter", sections: [{ summary: "Summary", evidence_ids: ["fact-0"], questions: [] }],
    chunks: 1, model: "fixture", created_at: "",
  } }], findings: [] };
  let ids;
  const memory = mount("frontend/src/features/wiki/StoryMemory.tsx", "StoryMemory", {
    project, view: "memory", onProject() {}, onChapter() {}, onReview: (selected) => { ids = selected; },
  }, { "../../lib/api": { api: async () => health, date: String, message: String } });
  const wiki = mount("frontend/src/features/wiki/Wiki.tsx", "Wiki", { project, onProject() {}, onChapter() {} });
  try {
    await memory.render();
    action(memory.tree, "Review 1 cited evidence records").props.onClick();
    assert.deepEqual(ids, ["fact-0"]);
    await wiki.render();
    action(wiki.tree, "Story memory").props.onClick();
    await wiki.render();
    const child = nodes(wiki.tree).find((node) => node.type?.displayName === "StoryMemory");
    assert.ok(child);
    child.props.onReview(ids);
    await wiki.render();
    assert.match(text(wiki.tree), /CITED_FACT/);
    assert.doesNotMatch(text(wiki.tree), /UNRELATED_FACT/);
    action(wiki.tree, "Show all pending evidence").props.onClick();
    await wiki.render();
    assert.match(text(wiki.tree), /UNRELATED_FACT/);
  } finally {
    memory.close();
    wiki.close();
  }
});
