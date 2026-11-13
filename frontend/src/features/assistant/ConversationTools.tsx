import { useEffect, useState } from "react";
import { IconArchive, IconGitBranch, IconPlus } from "@tabler/icons-react";
import { t } from "../../lib/i18n";
import { Button, ErrorBox, Modal } from "../../components/ui";
import { message } from "../../lib/api";
import type { useConversations } from "./conversations";

/** Present server-searched conversations and deliberate archive/restore actions. */
export function ConversationTools({
  manager,
  disabled,
}: {
  manager: ReturnType<typeof useConversations>;
  disabled: boolean;
}) {
  const [visible, setVisible] = useState(false);
  const [query, setQuery] = useState("");
  const [archived, setArchived] = useState(false);
  const [title, setTitle] = useState("");
  const [renaming, setRenaming] = useState(false);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    if (manager.manageVersion) setVisible(true);
  }, [manager.manageVersion]);
  useEffect(() => {
    if (!visible) return;
    const timer = setTimeout(() => {
      void manager.search(query, archived).catch((e) => setError(message(e)));
    }, 250);
    return () => clearTimeout(timer);
  }, [query, archived, visible]);

  /** Keep controls stable until the selected session operation has finished. */
  async function operate(operation: () => Promise<unknown>, close = true) {
    setError("");
    setBusy(true);
    try {
      await operation();
      if (close) {
        setVisible(false);
        setRenaming(false);
      }
    } catch (error) {
      setError(message(error));
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="conversation-tools">
      <button
        className="text-button conversation-title"
        title={t(manager.session?.title || "Conversations")}
        disabled={disabled || manager.loading || busy}
        onClick={() => setVisible(true)}
      >
        <IconGitBranch size={15} />
        <span>{t(manager.session?.title || "Conversations")}</span>
      </button>
      <button
        className="icon-button"
        aria-label={t("New conversation")}
        title={t("New conversation")}
        disabled={disabled || manager.loading || busy}
        onClick={() => void operate(() => manager.create())}
      >
        <IconPlus size={17} />
      </button>
      {manager.session && (
        <button
          className="text-button"
          disabled={disabled || busy}
          onClick={() => {
            setTitle(manager.session!.title);
            setRenaming(true);
          }}
        >
          {t("Rename conversation")}
        </button>
      )}
      {error && !visible && <ErrorBox>{error}</ErrorBox>}
      {visible && (
        <Modal title={t("Conversations")} onClose={() => setVisible(false)}>
          <div className="modal-content">
            <input
              aria-label={t("Search conversations")}
              placeholder={t("Search all saved conversations")}
              value={query}
              onChange={(event) => setQuery(event.target.value)}
            />
            <label className="checkbox-label">
              <input
                type="checkbox"
                checked={archived}
                onChange={(event) => setArchived(event.target.checked)}
              />
              {t("Archived conversations")}
            </label>
            {manager.loadingMore && (
              <p className="muted" role="status">
                {t("Loading…")}
              </p>
            )}
            <div className="session-list">
              {manager.sessions.map((item) => (
                <div className="chat-session-row" key={item.id}>
                  <button
                    className={`history-row ${manager.session?.id === item.id ? "selected" : ""}`}
                    disabled={busy}
                    onClick={() =>
                      void operate(async () => {
                        if (item.archived)
                          await manager.archive(item.id, false);
                        await manager.select(item.id);
                      })
                    }
                  >
                    <strong>{t(item.title)}</strong>
                    <small>
                      {item.turns.length} ·{" "}
                      {item.parent_id ? t("Branch") : t("Conversation")}
                    </small>
                  </button>
                  <button
                    className="icon-button"
                    disabled={busy}
                    aria-label={t(
                      item.archived
                        ? "Restore conversation"
                        : "Archive conversation",
                    )}
                    title={t(
                      item.archived
                        ? "Restore conversation"
                        : "Archive conversation",
                    )}
                    onClick={() =>
                      void operate(
                        () => manager.archive(item.id, !item.archived),
                        false,
                      )
                    }
                  >
                    <IconArchive size={17} />
                  </button>
                </div>
              ))}
            </div>
            {!manager.sessions.length && !manager.loadingMore && (
              <p className="muted">{t("No conversations found")}</p>
            )}
            {error && <ErrorBox>{error}</ErrorBox>}
            {manager.hasMore && (
              <Button
                disabled={manager.loadingMore || busy}
                onClick={() => void operate(manager.loadMore, false)}
              >
                {t("Load older conversations")}
              </Button>
            )}
          </div>
        </Modal>
      )}
      {renaming && (
        <Modal
          title={t("Rename conversation")}
          onClose={() => setRenaming(false)}
        >
          <form
            className="modal-content"
            onSubmit={(event) => {
              event.preventDefault();
              void operate(() => manager.rename(title));
            }}
          >
            <label>
              {t("Conversation title")}
              <input
                value={title}
                onChange={(event) => setTitle(event.target.value)}
                maxLength={120}
                required
              />
            </label>
            <Button primary type="submit" disabled={busy}>
              {t("Save")}
            </Button>
            {error && <ErrorBox>{error}</ErrorBox>}
          </form>
        </Modal>
      )}
    </div>
  );
}
