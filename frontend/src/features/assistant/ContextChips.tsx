import { useState } from "react";
import { IconX } from "@tabler/icons-react";
import { t } from "../../lib/i18n";
import type { TurnChip } from "./turnContext";

/** Keep carried records out of the way until the author wants to remove one. */
export function ContextChips({
  chips,
  onRemove,
}: {
  chips: TurnChip[];
  onRemove: (id: string) => void;
}) {
  const [open, setOpen] = useState(false);
  if (!chips.length) return null;
  return (
    <div className="composer-context">
      <button
        type="button"
        className="composer-context-toggle"
        aria-expanded={open}
        onClick={() => setOpen((current) => !current)}
      >
        {t(open ? "Hide carried records" : "Carried records")}
        <span>{chips.length}</span>
      </button>
      {open && (
        <div
          className="composer-context-list"
          aria-label={t("What this turn carries")}
        >
          {chips.map((chip) => (
            <span className="composer-chip" key={chip.id}>
              <span>{chip.label}</span>
              <button
                type="button"
                aria-label={t("Remove {0} from this turn", chip.label)}
                onClick={() => onRemove(chip.id)}
              >
                <IconX size={12} />
              </button>
            </span>
          ))}
        </div>
      )}
    </div>
  );
}
