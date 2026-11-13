export interface ReferenceChapter {
  title: string;
  start: number;
  end: number;
  line: number;
  endLine: number;
}

/** Decode supported local novel encodings strictly, preserving the original text for citation. */
export function decodeReferenceText(buffer: ArrayBuffer) {
  const bytes = new Uint8Array(buffer);
  let encoding = "utf-8";
  if (bytes[0] === 0xff && bytes[1] === 0xfe) encoding = "utf-16le";
  else if (bytes[0] === 0xfe && bytes[1] === 0xff) encoding = "utf-16be";
  try {
    return {
      text: new TextDecoder(encoding, { fatal: true }).decode(buffer),
      encoding,
    };
  } catch {
    return {
      text: new TextDecoder("gb18030", { fatal: true }).decode(buffer),
      encoding: "gb18030",
    };
  }
}

/** Locate chapter ranges by original offset and line while retaining duplicate chapter numbers as distinct text. */
export function referenceChapters(text: string): ReferenceChapter[] {
  const headings =
    /^[\t \u3000]*第[零〇一二三四五六七八九十百千万两\d]+[章回][^\r\n]{0,110}(?:\r?\n|$)/gm;
  const chapters: ReferenceChapter[] = [];
  let previousOffset = 0;
  let line = 1;
  for (const match of text.matchAll(headings)) {
    const start = match.index!;
    line += text.slice(previousOffset, start).split("\n").length - 1;
    const previous = chapters[chapters.length - 1];
    if (previous) {
      previous.end = start;
      previous.endLine = line - 1;
    }
    chapters.push({
      title: match[0].trim(),
      start,
      end: text.length,
      line,
      endLine: 0,
    });
    previousOffset = start;
  }
  if (chapters.length)
    chapters[chapters.length - 1].endLine = text.split("\n").length;
  return chapters;
}
