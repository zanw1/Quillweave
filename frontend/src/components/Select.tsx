import {
  Children,
  Fragment,
  isValidElement,
  useEffect,
  useCallback,
  useLayoutEffect,
  useId,
  useRef,
  useState,
  type ReactNode,
  type SelectHTMLAttributes,
  type ChangeEvent,
} from "react";
import { createPortal } from "react-dom";
import { IconCheck, IconChevronDown } from "@tabler/icons-react";
import { t } from "../lib/i18n";

interface Option {
  value: string;
  label: string;
  disabled: boolean;
  group: string;
}

/** Collect accessible option labels while retaining existing grouped select declarations. */
function optionsFrom(
  children: ReactNode,
  group = "",
  disabled = false,
): Option[] {
  const result: Option[] = [];
  Children.forEach(children, (child) => {
    if (
      !isValidElement<{
        children?: ReactNode;
        value?: string | number;
        label?: string;
        disabled?: boolean;
      }>(child)
    )
      return;
    if (child.type === "option")
      result.push({
        value: String(child.props.value ?? child.props.children ?? ""),
        label: Children.toArray(child.props.children).join(""),
        disabled: disabled || !!child.props.disabled,
        group,
      });
    else if (child.type === "optgroup" || child.type === Fragment)
      result.push(
        ...optionsFrom(
          child.props.children,
          child.props.label || group,
          disabled || !!child.props.disabled,
        ),
      );
  });
  return result;
}

/** Share searchable keyboard navigation and popup positioning across all application selectors. */
export function Select({
  children,
  value,
  defaultValue,
  onChange,
  disabled,
  className = "",
  name,
  id,
  ...props
}: SelectHTMLAttributes<HTMLSelectElement>) {
  const items = optionsFrom(children);
  const [local, setLocal] = useState(
    String(defaultValue ?? items[0]?.value ?? ""),
  );
  const selected = String(value ?? local);
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const [position, setPosition] = useState({
    top: 0,
    left: 0,
    width: 200,
    maxHeight: 260,
  });
  const trigger = useRef<HTMLButtonElement>(null);
  const popup = useRef<HTMLDivElement>(null);
  const search = useRef<HTMLInputElement>(null);
  const listId = useId();
  const filtered = items.filter((item) =>
    `${item.label} ${item.group}`
      .toLocaleLowerCase()
      .includes(query.toLocaleLowerCase()),
  );
  const current = items.find((item) => item.value === selected);

  /** Position the popup inside the viewport without clipping it to a form or drawer. */
  function show() {
    if (disabled || !trigger.current) return;
    setQuery("");
    const selectedIndex = items.findIndex(
      (item) => item.value === selected && !item.disabled,
    );
    setActive(
      selectedIndex >= 0
        ? selectedIndex
        : items.findIndex((item) => !item.disabled),
    );
    setOpen(true);
  }

  /** Anchor from natural content size, independently of scrolling and the previous clipped height. */
  const positionPopup = useCallback(() => {
    if (!trigger.current || !popup.current) return;
    const box = trigger.current.getBoundingClientRect();
    const style = getComputedStyle(popup.current);
    const list = popup.current.querySelector<HTMLElement>(".qw-select-options");
    const searchStyle = search.current && getComputedStyle(search.current);
    // Use uncapped content height: measuring the clipped popup can flip its placement on each render.
    const naturalHeight =
      (list?.scrollHeight ?? 0) +
      (search.current?.offsetHeight ?? 0) +
      parseFloat(searchStyle?.marginBottom || "0") +
      parseFloat(style.paddingTop) +
      parseFloat(style.paddingBottom) +
      parseFloat(style.borderTopWidth) +
      parseFloat(style.borderBottomWidth);
    const below = window.innerHeight - box.bottom - 12;
    const above = box.top - 12;
    const downward = below >= Math.min(320, naturalHeight) || below >= above;
    const maxHeight = Math.max(64, Math.min(320, downward ? below : above));
    const width = Math.min(Math.max(230, box.width), window.innerWidth - 16);
    const next = {
      top: downward
        ? box.bottom + 6
        : Math.max(8, box.top - Math.min(naturalHeight, maxHeight) - 6),
      left: Math.max(8, Math.min(box.left, window.innerWidth - width - 8)),
      width,
      maxHeight,
    };
    setPosition((previous) =>
      Object.keys(next).every(
        (key) =>
          previous[key as keyof typeof next] === next[key as keyof typeof next],
      )
        ? previous
        : next,
    );
  }, []);

  useLayoutEffect(() => {
    if (open) positionPopup();
  }, [open, query, items.length, position.width, positionPopup]);

  /** Notify existing controlled form handlers and restore trigger focus after a selection. */
  function choose(item: Option) {
    if (item.disabled) return;
    setLocal(item.value);
    onChange?.({
      target: { value: item.value, name },
      currentTarget: { value: item.value, name },
    } as ChangeEvent<HTMLSelectElement>);
    setOpen(false);
    trigger.current?.focus();
  }

  useEffect(() => {
    if (!open) return;
    search.current?.focus();
    /** Close a popup when focus or pointer leaves both its trigger and portal. */
    function closeOutside(event: Event) {
      if (
        !trigger.current?.contains(event.target as Node) &&
        !popup.current?.contains(event.target as Node)
      )
        setOpen(false);
    }
    /** Close stale positioning when the page viewport changes. */
    function close() {
      setOpen(false);
    }
    /** Follow a visible anchor; queued scroll events from opening must not discard a valid menu. */
    function closeOnScroll(event: Event) {
      if (popup.current?.contains(event.target as Node)) return;
      const box = trigger.current?.getBoundingClientRect();
      if (
        !box ||
        box.bottom < 0 ||
        box.top > window.innerHeight ||
        box.right < 0 ||
        box.left > window.innerWidth
      )
        close();
      else positionPopup();
    }
    document.addEventListener("pointerdown", closeOutside);
    document.addEventListener("focusin", closeOutside);
    window.addEventListener("resize", close);
    document.addEventListener("scroll", closeOnScroll, true);
    return () => {
      document.removeEventListener("pointerdown", closeOutside);
      document.removeEventListener("focusin", closeOutside);
      window.removeEventListener("resize", close);
      document.removeEventListener("scroll", closeOnScroll, true);
    };
  }, [open, positionPopup]);

  return (
    <span className={`qw-select ${className}`}>
      {name && <input type="hidden" name={name} value={selected} />}
      <button
        type="button"
        id={id}
        ref={trigger}
        role="combobox"
        aria-label={props["aria-label"]}
        aria-labelledby={props["aria-labelledby"]}
        aria-describedby={props["aria-describedby"]}
        aria-invalid={props["aria-invalid"]}
        aria-required={props.required}
        tabIndex={props.tabIndex}
        aria-haspopup="listbox"
        aria-controls={open ? listId : undefined}
        aria-expanded={open}
        disabled={disabled}
        title={props.title}
        className="qw-select-trigger"
        onClick={() => (open ? setOpen(false) : show())}
        onKeyDown={(event) => {
          if (["ArrowDown", "ArrowUp", "Enter", " "].includes(event.key)) {
            event.preventDefault();
            show();
          }
        }}
      >
        <span>{current?.label || selected || t("Choose an option")}</span>
        <IconChevronDown size={15} />
      </button>
      {open &&
        createPortal(
          <div
            ref={popup}
            className="qw-select-popup"
            style={{ ...position, position: "fixed" }}
            onKeyDown={(event) => {
              if (event.nativeEvent.isComposing) return;
              if (event.key === "Escape") {
                event.preventDefault();
                event.stopPropagation();
                setOpen(false);
                trigger.current?.focus();
              }
              if (event.key === "ArrowDown" || event.key === "ArrowUp") {
                event.preventDefault();
                const direction = event.key === "ArrowDown" ? 1 : -1;
                let next = active < 0 ? (direction > 0 ? -1 : 0) : active;
                let enabled = -1;
                for (let n = 0; n < filtered.length; n++) {
                  next = (next + direction + filtered.length) % filtered.length;
                  if (!filtered[next].disabled) {
                    enabled = next;
                    break;
                  }
                }
                setActive(enabled);
                document
                  .getElementById(`${listId}-${enabled}`)
                  ?.scrollIntoView({ block: "nearest" });
              }
              if (event.key === "Tab") {
                setOpen(false);
                trigger.current?.focus();
              }
              if (event.key === "Enter") {
                event.preventDefault();
                if (filtered[active]) choose(filtered[active]);
              }
            }}
          >
            <input
              ref={search}
              aria-label={t("Search options")}
              placeholder={t("Search options")}
              value={query}
              onChange={(event) => {
                const value = event.target.value;
                setQuery(value);
                setActive(
                  items
                    .filter((item) =>
                      `${item.label} ${item.group}`
                        .toLocaleLowerCase()
                        .includes(value.toLocaleLowerCase()),
                    )
                    .findIndex((item) => !item.disabled),
                );
              }}
              aria-controls={listId}
              aria-activedescendant={
                filtered[active] ? `${listId}-${active}` : undefined
              }
            />
            <div
              role="listbox"
              id={listId}
              aria-label={props["aria-label"] || t("Options")}
              className="qw-select-options"
            >
              {!filtered.length && (
                <p className="muted">{t("No matching option")}</p>
              )}
              {filtered.map((item, index) => (
                <div key={`${item.value}-${index}`}>
                  {item.group &&
                    (index === 0 ||
                      filtered[index - 1].group !== item.group) && (
                      <p className="qw-select-group">{item.group}</p>
                    )}
                  <button
                    type="button"
                    tabIndex={-1}
                    role="option"
                    id={`${listId}-${index}`}
                    aria-selected={selected === item.value}
                    aria-disabled={item.disabled}
                    disabled={item.disabled}
                    className={`qw-select-option ${active === index ? "highlighted" : ""}`}
                    onMouseEnter={() => setActive(index)}
                    onClick={() => choose(item)}
                  >
                    <span>{item.label}</span>
                    {selected === item.value && <IconCheck size={16} />}
                  </button>
                </div>
              ))}
            </div>
          </div>,
          trigger.current?.closest("dialog[open]") || document.body,
        )}
    </span>
  );
}
