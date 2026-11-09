import { t } from "../../lib/i18n";
import { Button } from "../../components/ui";
import { wholeSetupRequest } from "./setupDrafts";

/** Ask the partner to review every setting, then wait for confirmation. */
export function ReviewSetupButton({
  onAsk,
}: {
  onAsk: (text: string, action?: string, intent?: string) => void;
}) {
  return (
    <Button
      onClick={() => onAsk(wholeSetupRequest(), "agent", "commit_extract")}
    >
      {t("Review the whole setup")}
    </Button>
  );
}
