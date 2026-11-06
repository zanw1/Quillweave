import { useState } from "react";
import { IconWorldSearch } from "@tabler/icons-react";
import { Button, ErrorBox, Modal } from "../../components/ui";
import { Select } from "../../components/Select";
import { t } from "../../lib/i18n";
import { useSearchSettings } from "../settings/search/state";

/** Keep one-request search permission compact while preserving the global kill switch and saved model binding. */
export function SearchRequestChoice({
  value,
  onChange,
  disabled,
}: {
  value: string;
  onChange: (value: string) => void;
  disabled: boolean;
}) {
  const [open, setOpen] = useState(false);
  const metadata = useSearchSettings();
  const settings = metadata.view?.settings;
  return (
    <>
      <Button
        size="sm"
        disabled={disabled}
        title={t("Search profile for this request")}
        onClick={() => setOpen(true)}
      >
        <IconWorldSearch size={16} />
        {t(value === "off" ? "Search off" : "Search choice")}
      </Button>
      {open && (
        <Modal
          title={t("Search profile for this request")}
          onClose={() => setOpen(false)}
        >
          <div className="modal-content studio-form">
            {metadata.error && <ErrorBox>{metadata.error}</ErrorBox>}
            <p className="muted">
              {t(
                "This permission applies to the next Agent request. Research citations remain separate from confirmed story facts.",
              )}
            </p>
            {settings && !settings.enabled && (
              <p className="studio-notice">
                {t(
                  "Web search is disabled globally. Open Search settings to enable it.",
                )}
              </p>
            )}
            <label>
              {t("Search profile for this request")}
              <Select
                aria-label={t("Search profile for this request")}
                value={value}
                onChange={(event) => onChange(event.target.value)}
              >
                <option value="inherit">
                  {t("Inherit model and default search")}
                </option>
                <option value="off">{t("Disable web search")}</option>
                {settings?.profiles.map((profile) => (
                  <option key={profile.id} value={profile.id}>
                    {profile.name}
                    {profile.enabled ? "" : ` · ${t("Disabled")}`}
                  </option>
                ))}
              </Select>
            </label>
          </div>
          <div className="modal-actions">
            <Button primary onClick={() => setOpen(false)}>
              {t("Done")}
            </Button>
          </div>
        </Modal>
      )}
    </>
  );
}
