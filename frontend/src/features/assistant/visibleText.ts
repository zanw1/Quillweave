/** Keep prose surrounding tool envelopes, including text after the final structured operation. */
export function visibleAssistantText(text: string) {
  if (!text.includes('"tool"')) return text;
  const pieces: string[] = [];
  let cursor = 0;
  let scan = 0;
  let found = false;
  while (scan < text.length) {
    const start = text.indexOf("{", scan);
    if (start < 0) break;
    const end = balancedObjectEnd(text, start);
    if (end < 0) break;
    const step = recoverStep(text.slice(start, end));
    if (!step || !tools.has(step.tool)) {
      scan = start + 1;
      continue;
    }
    const before = text
      .slice(cursor, start)
      .replace(/```(?:json)?\s*$/i, "")
      .trim();
    if (before) pieces.push(before);
    const visible = visibleProse(step);
    if (visible) pieces.push(visible);
    cursor = end;
    const closingFence = /^\s*```/.exec(text.slice(cursor));
    if (closingFence) cursor += closingFence[0].length;
    scan = cursor;
    found = true;
  }
  if (found) {
    const tail = text.slice(cursor).trim();
    if (tail) pieces.push(tail);
    return pieces.join("\n\n");
  }
  const step = recoverStep(text.trim());
  if (!step || !tools.has(step.tool)) return text;
  const start = text.indexOf("{");
  const before =
    start > 0
      ? text
          .slice(0, start)
          .replace(/```(?:json)?\s*$/i, "")
          .trim()
      : "";
  return [before, visibleProse(step)].filter(Boolean).join("\n\n");
}

/** Find an object's boundary while respecting braces inside JSON strings. */
function balancedObjectEnd(text: string, start: number) {
  let depth = 0;
  let inString = false;
  let escaped = false;
  for (let index = start; index < text.length; index++) {
    const char = text[index];
    if (inString) {
      if (escaped) escaped = false;
      else if (char === "\\") escaped = true;
      else if (char === '"') inString = false;
    } else if (char === '"') inString = true;
    else if (char === "{") depth++;
    else if (char === "}" && --depth === 0) return index + 1;
  }
  return -1;
}

interface Step {
  tool: string;
  text: string;
  title: string;
  content: string;
}

/** Recover one display-only envelope without changing its stored source. */
function recoverStep(text: string): Step | null {
  const cleaned = text
    .replace(/^```(?:json)?/i, "")
    .replace(/```$/, "")
    .trim();
  const parsed = parseObject(cleaned) || parseObject(repairControls(cleaned));
  if (parsed) return parsed;
  return scrape(cleaned);
}

/** Decode a complete envelope and keep only its author-facing fields. */
function parseObject(text: string): Step | null {
  try {
    const value = JSON.parse(text) as {
      tool?: string;
      text?: string;
      proposal?: { title?: string; content?: string };
      proposals?: { title?: string; content?: string }[];
    };
    const proposal = value.proposal || value.proposals?.[0];
    if (!value.tool && !proposal) return null;
    return {
      tool: value.tool || "",
      text: value.text || "",
      title: proposal?.title || "",
      content: proposal?.content || "",
    };
  } catch {
    return null;
  }
}

/** Escape literal control characters inside JSON string values. */
function repairControls(text: string) {
  let out = "";
  let inString = false;
  let escaped = false;
  for (const ch of text) {
    if (!inString) {
      if (ch === '"') inString = true;
      out += ch;
      continue;
    }
    if (escaped) {
      out += ch;
      escaped = false;
      continue;
    }
    if (ch === "\\") {
      out += ch;
      escaped = true;
    } else if (ch === '"') {
      inString = false;
      out += ch;
    } else if (ch === "\n") out += "\\n";
    else if (ch === "\r") out += "\\r";
    else if (ch === "\t") out += "\\t";
    else out += ch;
  }
  return out;
}

const tools = new Set([
  "recall",
  "get_entity_detail",
  "get_chapter_window",
  "search_story_facts",
  "get_plan_node",
  "get_world_map",
  "get_source_detail",
  "get_decision_detail",
  "get_conversation_excerpt",
  "get_task_step",
  "search",
  "plan",
  "propose",
  "propose_changes",
  "researcher",
  "reviewer",
  "writer",
  "ask_author",
  "finish",
]);

/** Recover author-facing fields from a recognized malformed tool envelope. */
function scrape(text: string): Step | null {
  const tool = field(text, "tool");
  if (!tool || !tools.has(tool)) return null;
  return {
    tool,
    text: field(text, "text"),
    title: field(text, "title"),
    content: field(text, "content"),
  };
}

/** Read one quoted field while preserving escaped quotes. */
function field(text: string, key: string) {
  const marker = `"${key}"`;
  const at = text.indexOf(marker);
  if (at < 0) return "";
  let rest = text.slice(at + marker.length).trimStart();
  if (!rest.startsWith(":")) return "";
  rest = rest.slice(1).trimStart();
  if (!rest.startsWith('"')) return "";
  const body = rest.slice(1);
  let end = body.length;
  for (let i = 0; i < body.length; i++) {
    if (body[i] !== '"' || oddSlash(body, i)) continue;
    const tail = body.slice(i + 1).trimStart();
    if (
      tail === "" ||
      tail.startsWith("}") ||
      tail.startsWith(",") ||
      tail.startsWith("`") ||
      tail.startsWith("json")
    ) {
      end = i;
      break;
    }
  }
  return unescape(body.slice(0, end)).trim();
}

/** Determine whether a quote follows an odd number of escape characters. */
function oddSlash(text: string, quote: number) {
  let slashes = 0;
  for (let i = quote - 1; i >= 0 && text[i] === "\\"; i--) slashes++;
  return slashes % 2 === 1;
}

/** Decode only supported JSON escapes in recovered prose. */
function unescape(value: string) {
  return value.replace(/\\(u[0-9a-fA-F]{4}|["\\/bfnrt])/g, (token) => {
    switch (token[1]) {
      case "n":
        return "\n";
      case "r":
        return "\r";
      case "t":
        return "\t";
      case "u":
        return String.fromCharCode(Number.parseInt(token.slice(2), 16));
      default:
        return token[1];
    }
  });
}

/** Choose the question, answer or proposal prose that belongs in the transcript. */
function visibleProse(step: Step) {
  if (
    step.tool &&
    step.tool !== "ask_author" &&
    step.tool !== "finish" &&
    step.tool !== "propose" &&
    step.tool !== "propose_changes"
  ) {
    return "";
  }
  if (step.text && step.tool !== "propose" && step.tool !== "propose_changes")
    return step.text;
  if (step.title && step.content) return `### ${step.title}\n\n${step.content}`;
  return step.content || step.text || step.title;
}
/** Keep structured writing-stage payloads in task records while rendering their dedicated cards. */
export function isVisibleChatActivity(item: { kind: string; purpose?: string }) {
  return item.kind !== "text" || !["writing_composition", "writing_review"].includes(item.purpose || "");
}

/** Display generated follow-up requests concisely while preserving their full payload in request inspection. */
export function visibleAuthorInstruction(text: string, intent?: string) {
  return ["writing_recompose", "writing_trial", "writing_revision"].includes(intent || "")
    ? text.split("\n", 1)[0]
    : text;
}
