import { getLanguage, t } from "../lib/i18n";
import { useMemo, type ComponentPropsWithoutRef } from "react";
import {
  Streamdown,
  defaultComponents,
  defaultRehypePlugins,
  defaultTranslations,
  type Components,
  type ExtraProps,
  type StreamdownTranslations,
} from "streamdown";
import { cjk } from "@streamdown/cjk";
import "../styles/streamdown.css";

const markdownPlugins = { cjk };
const markdownRehypePlugins = [
  defaultRehypePlugins.sanitize,
  defaultRehypePlugins.harden,
];
const markdownLinkSafety = { enabled: true };

/** Keep in-document references as native anchors while retaining URL review for external links. */
function MarkdownLink(properties: unknown) {
  const { node: _node, ...props } =
    properties as ComponentPropsWithoutRef<"a"> & ExtraProps;
  const Link = defaultComponents.a;
  return props.href?.startsWith("#") ? (
    <a {...props} target="_self" />
  ) : (
    <Link {...props} />
  );
}

/** Display image references as author-opened links instead of fetching remote content automatically. */
function MarkdownImageReference(properties: unknown) {
  const { src, alt } = properties as { src?: unknown; alt?: unknown };
  const caption =
    typeof alt === "string" && alt ? alt : t("Open image reference");
  const Link = defaultComponents.a;
  return typeof src === "string" && /^https?:\/\//i.test(src) ? (
    <span className="markdown-image-reference">
      <Link href={src}>{caption}</Link>
    </span>
  ) : (
    <span className="markdown-image-reference">
      {typeof alt === "string" && alt ? alt : t("Image reference")}
    </span>
  );
}

const markdownComponents: Components = {
  a: MarkdownLink,
  img: MarkdownImageReference,
};

/** Render streamed and historical AI Markdown while leaving the stored and copied source untouched. */
export default function StreamdownResponse({
  text,
  streaming = false,
  incomplete = false,
}: {
  text: string;
  streaming?: boolean;
  incomplete?: boolean;
}) {
  const language = getLanguage();
  const translations = useMemo(
    () =>
      Object.fromEntries(
        Object.entries(defaultTranslations).map(([key, label]) => [
          key,
          t(label),
        ]),
      ) as Partial<StreamdownTranslations>,
    [language],
  );
  const markdownOptions = useMemo(
    () => ({
      footnoteLabel: t("Footnotes"),
      footnoteBackLabel: t("Back to reference"),
    }),
    [language],
  );
  return (
    <div
      className="answer-text markdown-response"
      data-streaming={streaming || undefined}
    >
      <Streamdown
        mode={streaming || incomplete ? "streaming" : "static"}
        isAnimating={streaming}
        parseIncompleteMarkdown={streaming || incomplete}
        plugins={markdownPlugins}
        rehypePlugins={markdownRehypePlugins}
        remarkRehypeOptions={markdownOptions}
        components={markdownComponents}
        translations={translations}
        controls={false}
        linkSafety={markdownLinkSafety}
        dir="auto"
        caret={streaming ? "circle" : undefined}
      >
        {text}
      </Streamdown>
    </div>
  );
}
