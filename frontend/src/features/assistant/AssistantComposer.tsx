import {
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type KeyboardEvent,
  type RefObject,
} from "react";
import { createPortal } from "react-dom";
import {
  IconArrowUp,
  IconPlayerStop,
  IconSlash,
  IconX,
} from "@tabler/icons-react";
import { Button, ErrorBox } from "../../components/ui";
import { t } from "../../lib/i18n";
import type {
  ChatModelSelection,
  ChatSkill,
  ModelSettings,
  Prompt,
  ProviderCatalog,
} from "../../domain/types";
import { ChatModelControls } from "./ChatModelControls";
import { ContextChips } from "./ContextChips";
import { commandSuggestions, completeChatCommand } from "./chatCommands";

interface Props {
  input: string;
  onInput: (value: string) => void;
  onSubmit: () => void;
  onStop: () => void;
  onCommand: (name: string) => void;
  onRecall: () => void;
  busy: boolean;
  running: boolean;
  loading: boolean;
  error: string;
  prompt: Prompt | null;
  chips: { id: string; label: string }[];
  onRemoveChip: (id: string) => void;
  selectedSkills: string[];
  skills: ChatSkill[];
  onRemoveSkill: (id: string) => void;
  settings: ModelSettings | null;
  catalog: ProviderCatalog | null;
  selection?: ChatModelSelection;
  onSelection: (selection: ChatModelSelection) => void;
  modelOpen: boolean;
  onModelOpen: (open: boolean) => void;
  onSettings: () => void;
  composerRef: RefObject<HTMLDivElement | null>;
  resized: boolean;
  searchLabel?: string;
  commitPolicy: "task" | "review" | "auto";
  onCommitPolicy: (value: "task" | "review" | "auto") => void;
}

/** Compose requests with a registry-backed slash menu and visible model/token choices. */
export function AssistantComposer(props: Props) {
  const [menuIndex, setMenuIndex] = useState(0);
  const [dismissed, setDismissed] = useState("");
  const [toolsOpen, setToolsOpen] = useState(false);
  const textarea = useRef<HTMLTextAreaElement>(null);
  const menuId = useId();
  const suggestions = toolsOpen
    ? commandSuggestions("/")
    : dismissed === props.input ? [] : commandSuggestions(props.input);
  useEffect(() => setMenuIndex(0), [props.input]);
  const budget = props.prompt?.budget;
  const contextUsage = budget?.context_window_tokens
    ? Math.min(1, budget.input_tokens_estimate / budget.context_window_tokens)
    : 0;
  const contextLabel = budget
    ? `${budget.estimated ? "~" : ""}${budget.input_tokens_estimate.toLocaleString()} / ${budget.context_window_tokens ? budget.context_window_tokens.toLocaleString() : t("Unknown")} tokens`
    : t("Context");
  const [menuBox, setMenuBox] = useState<{
    top: number;
    left: number;
    width: number;
    maxHeight: number;
  } | null>(null);
  const menuOpen = suggestions.length > 0;
  useLayoutEffect(() => {
    if (!menuOpen) {
      setMenuBox(null);
      return;
    }
    /** Anchor the command palette inside the active dialog without clipping it in a resized composer. */
    function positionMenu() {
      const element = textarea.current;
      if (!element) return;
      const box = element.getBoundingClientRect();
      const dialogTop =
        element.closest("dialog[open]")?.getBoundingClientRect().top || 0;
      const above = Math.max(0, box.top - Math.max(12, dialogTop + 12) - 6);
      const below = Math.max(0, window.innerHeight - box.bottom - 16);
      const useAbove = above >= Math.min(180, below);
      const maxHeight = Math.max(
        60,
        Math.min(330, suggestions.length * 48 + 10, useAbove ? above : below),
      );
      const width = Math.min(box.width, window.innerWidth - 24);
      setMenuBox({
        top: useAbove ? Math.max(12, box.top - maxHeight - 6) : box.bottom + 6,
        left: Math.max(12, Math.min(box.left, window.innerWidth - width - 12)),
        width,
        maxHeight,
      });
    }
    positionMenu();
    const observer = new ResizeObserver(positionMenu);
    if (textarea.current) observer.observe(textarea.current);
    window.addEventListener("resize", positionMenu);
    window.addEventListener("scroll", positionMenu, true);
    return () => {
      observer.disconnect();
      window.removeEventListener("resize", positionMenu);
      window.removeEventListener("scroll", positionMenu, true);
    };
  }, [menuOpen, props.resized, suggestions.length]);
  useEffect(() => {
    if (menuOpen)
      document
        .getElementById(`${menuId}-${menuIndex}`)
        ?.scrollIntoView({ block: "nearest" });
  }, [menuIndex, menuOpen, menuId]);

  /** Complete a command token while preserving author control over submission. */
  function choose(name: string) {
    props.onInput(completeChatCommand(props.input, name));
    setToolsOpen(false);
    textarea.current?.focus();
  }

  /** Give command navigation priority while preserving IME composition and ordinary multiline input. */
  function onKeyDown(event: KeyboardEvent<HTMLTextAreaElement>) {
    if (event.nativeEvent.isComposing || event.keyCode === 229) return;
    if (
      suggestions.length &&
      ["ArrowDown", "ArrowUp", "Enter", "Tab", "Escape"].includes(event.key)
    ) {
      event.preventDefault();
      if (
        event.key === "Enter" &&
        suggestions.some((command) => `/${command.name}` === props.input.trim())
      ) {
        if (!props.busy && !props.running) props.onSubmit();
        return;
      }
      if (event.key === "Escape") {
        setDismissed(props.input);
        setToolsOpen(false);
      }
      else if (event.key === "ArrowDown")
        setMenuIndex((index) => (index + 1) % suggestions.length);
      else if (event.key === "ArrowUp")
        setMenuIndex(
          (index) => (index - 1 + suggestions.length) % suggestions.length,
        );
      else choose(suggestions[menuIndex]?.name || suggestions[0].name);
      return;
    }
    if (event.key === "ArrowUp" && !props.input) {
      event.preventDefault();
      props.onRecall();
    }
    if (event.key === "Enter" && !event.shiftKey) {
      event.preventDefault();
      if (!props.busy && !props.running) props.onSubmit();
    }
  }
  return (
    <div
      className={`assistant-composer chat-composer${props.resized ? " is-resized" : ""}`}
      ref={props.composerRef}
    >
      {props.error && <ErrorBox>{props.error}</ErrorBox>}
      <ContextChips chips={props.chips} onRemove={props.onRemoveChip} />
      {!!props.selectedSkills.length && (
        <div className="chat-selected-skills">
          {props.selectedSkills.map((id) => (
            <span className="composer-chip" key={id}>
              <span>
                {props.skills.find((skill) => skill.id === id)?.name || id}
              </span>
              <button
                aria-label={t("Remove skill")}
                onClick={() => props.onRemoveSkill(id)}
              >
                <IconX size={12} />
              </button>
            </span>
          ))}
        </div>
      )}
      <div className="chat-writing-tools" role="group" aria-label={t("Creation tools")}>
        <button type="button" onClick={() => choose("outline")}>{t("Outline")}</button>
        <button type="button" onClick={() => choose("write")}>{t("Write chapter")}</button>
        <button type="button" onClick={() => props.onCommand("search")}>{t("Search")}</button>
        <button type="button" onClick={() => props.onCommand("skill")}>{t("Skills")}</button>
        <label className="chat-commit-policy">
          <span>{t("Save output")}</span>
          <select
            aria-label={t("Save output")}
            value={props.commitPolicy}
            onChange={(event) => props.onCommitPolicy(event.target.value as Props["commitPolicy"])}
          >
            <option value="task">{t("By task")}</option>
            <option value="auto">{t("Save automatically")}</option>
            <option value="review">{t("Review before saving")}</option>
          </select>
        </label>
      </div>
      <div className="chat-input-shell">
        <div className="chat-input-wrap">
          {menuBox &&
            suggestions.length > 0 &&
            createPortal(
              <div
                className="chat-command-menu"
                style={menuBox}
                role="listbox"
                id={menuId}
                aria-label={t("Chat commands")}
              >
                {suggestions.map((command, index) => (
                  <button
                    type="button"
                    role="option"
                    id={`${menuId}-${index}`}
                    aria-selected={index === menuIndex}
                    className={index === menuIndex ? "is-selected" : ""}
                    key={command.name}
                    onMouseDown={(event) => event.preventDefault()}
                    onClick={() => choose(command.name)}
                  >
                    <span>
                      <strong>/{command.name}</strong>
                      <small>{t(command.group)}</small>
                    </span>
                    <span>{t(command.description)}</span>
                  </button>
                ))}
              </div>,
              textarea.current?.closest("dialog[open]") || document.body,
            )}
          <textarea
            ref={textarea}
            aria-label={t("Writing request")}
            aria-description={t("Enter to send · Shift+Enter for a new line")}
            aria-autocomplete="list"
            aria-controls={suggestions.length ? menuId : undefined}
            aria-expanded={suggestions.length > 0}
            aria-activedescendant={
              suggestions.length ? `${menuId}-${menuIndex}` : undefined
            }
            placeholder={t("Write a message, or type / for commands…")}
            value={props.input}
            disabled={props.loading}
            onChange={(event) => {
              setDismissed("");
              setToolsOpen(false);
              props.onInput(event.target.value);
            }}
            onKeyDown={onKeyDown}
          />
        </div>
        <div className="composer-footer chat-composer-footer">
          <button
            className="icon-button chat-command-trigger"
            title={t("Chat commands")}
            aria-label={t("Chat commands")}
            onClick={() => {
              setToolsOpen((open) => !open);
              textarea.current?.focus();
            }}
          >
            <IconSlash size={16} />
          </button>
          <div className="chat-composer-options">
            <button
              className="icon-button chat-token-usage"
              aria-label={t("Inspect context")}
              title={`${t("Inspect context")} · ${contextLabel}`}
              onClick={() => props.onCommand("context")}
            >
              <svg
                width="20"
                height="20"
                viewBox="0 0 20 20"
                fill="none"
                aria-hidden="true"
              >
                <circle
                  cx="10"
                  cy="10"
                  r="7"
                  stroke="currentColor"
                  strokeWidth="2.5"
                  opacity="0.2"
                />
                <circle
                  cx="10"
                  cy="10"
                  r="7"
                  stroke="currentColor"
                  strokeWidth="2.5"
                  strokeLinecap="round"
                  pathLength="1"
                  strokeDasharray={`${contextUsage} 1`}
                  transform="rotate(-90 10 10)"
                />
              </svg>
            </button>
            <ChatModelControls
              settings={props.settings}
              catalog={props.catalog}
              selection={props.selection}
              onChange={props.onSelection}
              onSettings={props.onSettings}
              expanded={props.modelOpen}
              onExpanded={props.onModelOpen}
            />
          </div>
          {props.running ? (
            <Button
              className="chat-send-button"
              aria-label={t("Stop")}
              title={t("Stop")}
              onClick={props.onStop}
            >
              <IconPlayerStop size={20} />
            </Button>
          ) : (
            <Button
              className="chat-send-button"
              aria-label={t("Send")}
              title={props.busy ? t("Preparing") : t("Send")}
              disabled={props.busy || props.loading || !props.input.trim()}
              onClick={props.onSubmit}
            >
              <IconArrowUp size={22} />
            </Button>
          )}
        </div>
      </div>
      <p className="chat-craft-note">{t(props.commitPolicy === "auto"
        ? "Save automatically after checks; conflicts remain available for review."
        : props.commitPolicy === "review"
          ? "Keep generated changes for review before saving."
          : "Fiction craft applied · creation commands save automatically; discussion stays for review.")}</p>
      {(budget?.needs_compaction || props.searchLabel) && (
        <p className="composer-note">
          {budget?.needs_compaction
            ? t("Earlier exchanges will be compacted before sending.")
            : props.searchLabel}
        </p>
      )}
    </div>
  );
}
