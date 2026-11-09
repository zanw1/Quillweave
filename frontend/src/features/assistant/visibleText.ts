/** Show the author's prose when a model wrapped it in a tool envelope. */
export function visibleAssistantText(text: string) {
  const trimmed = text.trim();
  if (!trimmed.includes('"tool"')) return text;
  const step = recoverStep(trimmed);
  if (!step) return text;
  const visible = visibleProse(step);
  const start = trimmed.indexOf("{");
  const before = start > 0 ? trimmed.slice(0, start).replace(/```json\s*$/i, "").replace(/```\s*$/, "").trim() : "";
  if (before && visible) return `${before}\n\n${visible}`;
  if (before) return before;
  return visible;
}

interface Step {
  tool: string;
  text: string;
  title: string;
  content: string;
}

function recoverStep(text: string): Step | null {
  const cleaned = text.replace(/^```(?:json)?/i, "").replace(/```$/, "").trim();
  const parsed = parseObject(cleaned) || parseObject(repairControls(cleaned));
  if (parsed) return parsed;
  return scrape(cleaned);
}

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

function oddSlash(text: string, quote: number) {
  let slashes = 0;
  for (let i = quote - 1; i >= 0 && text[i] === "\\"; i--) slashes++;
  return slashes % 2 === 1;
}

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
  if (step.text && step.tool !== "propose" && step.tool !== "propose_changes") return step.text;
  if (step.title && step.content) return `### ${step.title}\n\n${step.content}`;
  return step.content || step.text || step.title;
}
