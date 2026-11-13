import { useEffect, useState } from "react";
import { api, message } from "../../lib/api";
import { t } from "../../lib/i18n";
import { Button, ErrorBox, Modal } from "../../components/ui";
import { Select } from "../../components/Select";
import type { ChatSkill } from "../../domain/types";
import { useSearchSettings } from "../settings/search/state";

/** Configure explicit next-turn search without exposing disabled services as selectable choices. */
export function ChatSearchModal({
  value,
  onChange,
  onClose,
  onRequest,
}: {
  value: string;
  onChange: (value: string) => void;
  onClose: () => void;
  onRequest: () => void;
}) {
  const metadata = useSearchSettings();
  return (
    <Modal
      title={t("Web search")}
      onClose={onClose}
      footer={
        <>
          <Button onClick={onClose}>{t("Done")}</Button>
          <Button
            primary
            disabled={metadata.loading || !metadata.view?.settings.enabled || value === "off"}
            onClick={onRequest}
          >
            {t("Prepare search request")}
          </Button>
        </>
      }
    >
      <div className="modal-content studio-form">
        <p className="muted">
          {t(
            "Choose the service for your next submission. Actual searches and citations appear with the reply.",
          )}
        </p>
        {metadata.error && <ErrorBox>{metadata.error}</ErrorBox>}
        {metadata.loading && <p role="status">{t("Loading…")}</p>}
        {metadata.view && !metadata.view.settings.enabled && (
          <ErrorBox>
            {t(
              "Web search is disabled globally. Open Search settings to enable it.",
            )}
          </ErrorBox>
        )}
        <label>
          {t("Search profile for this request")}
          <Select
            value={value}
            disabled={metadata.loading}
            onChange={(event) => onChange(event.target.value)}
          >
            <option value="inherit">
              {t("Inherit model and default search")}
            </option>
            <option value="off">{t("Disable web search")}</option>
            {metadata.view?.settings.profiles
              .filter((profile) => profile.enabled)
              .map((profile) => (
                <option value={profile.id} key={profile.id}>
                  {profile.name}
                </option>
              ))}
          </Select>
        </label>
        <Button onClick={() => void metadata.reload().catch(() => {})}>
          {t("Reload")}
        </Button>
      </div>
    </Modal>
  );
}

/** Select a catalog-backed skill and edit explicit discovery roots independently of model requests. */
export function ChatSkillsModal({
  skills,
  selected,
  onChange,
  onReload,
  onClose,
}: {
  skills: ChatSkill[];
  selected: string[];
  onChange: (ids: string[]) => void;
  onReload: () => Promise<void>;
  onClose: () => void;
}) {
  const [query, setQuery] = useState("");
  const [roots, setRoots] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    let active = true;
    api<{ roots: string[] }>("list_skills")
      .then((result) => {
        if (active) setRoots((result.roots || []).join("\n"));
      })
      .catch((error) => {
        if (active) setError(message(error));
      });
    return () => {
      active = false;
    };
  }, []);

  /** Save only the roots typed by the author and refresh the current discovery catalog. */
  async function saveRoots() {
    setBusy(true);
    setError("");
    try {
      await api("set_skill_roots", {
        roots: roots
          .split("\n")
          .map((root) => root.trim())
          .filter(Boolean),
      });
      await onReload();
    } catch (error) {
      setError(message(error));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal
      title={t("Skills")}
      onClose={onClose}
      footer={
        <>
          <Button disabled={busy} onClick={() => void onReload()}>
            {t("Reload")}
          </Button>
          <Button primary onClick={onClose}>
            {t("Done")}
          </Button>
        </>
      }
    >
      <div className="modal-content studio-form">
        <p className="muted">
          {t(
            "Selected skills load their instructions into this turn. Their source and version remain attached to the request.",
          )}
        </p>
        <input
          aria-label={t("Search skills")}
          placeholder={t("Search skills")}
          value={query}
          onChange={(event) => setQuery(event.target.value)}
        />
        <div className="chat-skill-list">
          {skills
            .filter((skill) =>
              `${skill.name} ${skill.description}`
                .toLocaleLowerCase()
                .includes(query.toLocaleLowerCase()),
            )
            .map((skill) => (
              <label className="chat-skill-choice" key={skill.id}>
                <input
                  type="checkbox"
                  checked={selected.includes(skill.id)}
                  onChange={(event) =>
                    onChange(
                      event.target.checked
                        ? [...selected, skill.id]
                        : selected.filter((id) => id !== skill.id),
                    )
                  }
                />
                <span>
                  <strong>{skill.name}</strong>
                  <small>{skill.description}</small>
                  <small>
                    {skill.source} · {skill.version.slice(0, 12)}
                  </small>
                </span>
              </label>
            ))}
        </div>
        {!skills.length && (
          <p className="muted">
            {t(
              "No skills discovered. Add a directory containing skill folders below.",
            )}
          </p>
        )}
        <details className="chat-skill-directories">
          <summary>{t("Skill directories")}</summary>
          <div className="chat-skill-directory-fields">
            <label>
              {t("One directory per line")}
              <textarea
                aria-label={t("Skill directories")}
                value={roots}
                onChange={(event) => setRoots(event.target.value)}
                rows={4}
              />
            </label>
            <div className="chat-skill-directory-actions">
              <Button disabled={busy} onClick={() => void saveRoots()}>
                {t("Save and reload skills")}
              </Button>
            </div>
          </div>
        </details>
        {error && <ErrorBox>{error}</ErrorBox>}
      </div>
    </Modal>
  );
}
