import { useState } from "react";
import { IconGitBranch, IconPlus } from "@tabler/icons-react";
import { t } from "../../lib/i18n";
import { Button, ErrorBox, Modal } from "../../components/ui";
import { message } from "../../lib/api";
import type { useConversations } from "./conversations";

/** Expose named searchable sessions and deliberate branch selection beside the conversation. */
export function ConversationTools({
  manager,
  disabled,
}: {
  manager: ReturnType<typeof useConversations>;
  disabled: boolean;
}) {
  const [visible, setVisible] = useState(false);
  const [query, setQuery] = useState("");
  const [title, setTitle] = useState("");
  const [renaming, setRenaming] = useState(false);
  const [error, setError] = useState("");

  /** Surface recoverable session operations without blocking the writing workspace. */
  async function operate(operation: () => Promise<unknown>, close = true) {
    setError("");
    try {
      await operation();
      if (close) {
        setVisible(false);
        setRenaming(false);
      }
    } catch (error) {
      setError(message(error));
    }
  }

  return (
    <div className="conversation-tools">
      <button
        className="text-button"
        disabled={disabled || manager.loading}
        onClick={() => {
          setVisible(true);
          void operate(() => manager.reload(), false);
        }}
      >
        <IconGitBranch size={15} />
        {t(manager.session?.title || "Conversations")}
      </button>
      <button
        className="icon-button"
        aria-label={t("New conversation")}
        title={t("New conversation")}
        disabled={disabled || manager.loading}
        onClick={() => void operate(manager.create)}
      >
        <IconPlus size={17} />
      </button>
      {manager.session && (
        <button
          className="text-button"
          disabled={disabled}
          onClick={() => {
            setTitle(manager.session!.title);
            setRenaming(true);
          }}
        >
          {t("Rename conversation")}
        </button>
      )}
      {error && <ErrorBox>{error}</ErrorBox>}
      {visible && (
        <Modal title={t("Conversations")} onClose={() => setVisible(false)}>
          <div className="modal-content">
            <input
              aria-label={t("Search conversations")}
              placeholder={t("Search conversations")}
              value={query}
              onChange={(event) => setQuery(event.target.value)}
            />
            <div className="session-list">
              {manager.sessions
                .filter((item) =>
                  `${item.title} ${item.turns.map((turn) => turn.content).join(" ")}`
                    .toLocaleLowerCase()
                    .includes(query.toLocaleLowerCase()),
                )
                .map((item) => (
                  <button
                    key={item.id}
                    className={`history-row ${manager.session?.id === item.id ? "selected" : ""}`}
                    onClick={() => void operate(() => manager.select(item.id))}
                  >
                    <strong>
                      {item.title === "New conversation"
                        ? t(item.title)
                        : item.title}
                    </strong>
                    <small>
                      {item.turns.length} · {item.parent_id && t("Branch")}
                    </small>
                  </button>
                ))}
            </div>
            {error && <ErrorBox>{error}</ErrorBox>}
            {manager.hasMore && (
              <Button
                disabled={manager.loadingMore}
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
            <Button primary type="submit">
              {t("Save")}
            </Button>
            {error && <ErrorBox>{error}</ErrorBox>}
          </form>
        </Modal>
      )}
    </div>
  );
}
