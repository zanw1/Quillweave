import { t } from "../../lib/i18n";
import { useState } from "react";
import {
  IconExternalLink,
  IconSparkles,
  IconArrowUpRight,
  IconBook2,
} from "@tabler/icons-react";
import { Badge, Button, Drawer } from "../../components/ui";
import { methods } from "./catalog";

/** Present attributed writing methods as optional review tools. */
export function Methods({ onUse }: { onUse: (prompt: string) => void }) {
  const [stage, setStage] = useState("All");
  const [selected, setSelected] = useState<string | null>(null);
  const method = methods.find((m) => m.id === selected);
  return (
    <section className="workspace-page methods-page">
      <div className="section-title">
        <div>
          <p className="eyebrow">{t("THE WRITER'S TOOLKIT")}</p>
          <h1>{t("Methods for the story you want to tell")}</h1>
          <p>
            {t("Six writing tools to help you examine a story from another angle.")}
          </p>
        </div>
        <Badge>{t("Optional methods for exploration")}</Badge>
      </div>
      <div className="filter-chips">
        {["All", ...methods.map((m) => m.stage)].map((label) => (
          <button
            key={label}
            className={stage === label ? "active" : ""}
            onClick={() => setStage(label)}
          >
            {t(label)}
          </button>
        ))}
      </div>
      <div className="method-grid">
        {methods
          .filter((m) => stage === "All" || stage === m.stage)
          .map((m, index) => (
            <article className="method-card" key={m.id}>
              <div className="method-top">
                <span>0{index + 1}</span>
                <Badge tone="blue">{t(m.stage)}</Badge>
              </div>
              <h2>{t(m.title)}</h2>
              <h4>{t(m.subtitle)}</h4>
              <p>{t(m.description)}</p>
              <div className="method-card-footer">
                <span>{m.author}</span>
                <button
                  className="text-button"
                  onClick={() => setSelected(m.id)}
                >
                  {t("Explore method")}{" "}<IconArrowUpRight size={16} />
                </button>
              </div>
            </article>
          ))}
      </div>
      <div className="methods-note">
        <IconBook2 size={22} stroke={1.5} />
        <p>
          {t("Use these methods to ask sharper questions. Character choices, meaning, and final decisions belong to the author.")}
        </p>
      </div>
      {method && (
        <Drawer
          title={t(method.title)}
          subtitle={t("Writing methods / {0}", t(method.stage))}
          onClose={() => setSelected(null)}
        >
          <p className="drawer-lead">{t(method.description)}</p>
          <div className="drawer-section">
            <h3>{t("Questions for a closer reading")}</h3>
            {method.questions.map((question, index) => (
              <div className="question-row" key={question}>
                <span>{index + 1}</span>
                <p>{t(question)}</p>
              </div>
            ))}
          </div>
          <div className="drawer-section">
            <h3>{t("When to use it")}</h3>
            <p>{t(method.scope)}</p>
          </div>
          <div className="drawer-section">
            <h3>{t("Original source")}</h3>
            <a
              className="source-url"
              href={method.url}
              target="_blank"
              rel="noreferrer"
            >
              {method.author}
              <IconExternalLink size={16} />
            </a>
            <p className="muted">
              {t("This is a short summary with review questions. Read the original source for the full discussion.")}
            </p>
          </div>
          <Button
            primary
            onClick={() => {
              onUse(method.prompt);
              setSelected(null);
            }}
          >
            <IconSparkles size={17} />
            {t("Discuss these questions")}
          </Button>
        </Drawer>
      )}
    </section>
  );
}
