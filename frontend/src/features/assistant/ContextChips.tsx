import { IconX } from "@tabler/icons-react";
import { t } from "../../lib/i18n";
import type { TurnChip } from "./turnContext";

/** Show each record this turn will carry, and let the author remove one. */
export function ContextChips({
  chips,
  onRemove,
}: {
  chips: TurnChip[];
  onRemove: (id: string) => void;
}) {
  if (!chips.length) return null;
  return (
    <div className="composer-context" aria-label={t("What this turn carries")}>
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
  );
}
