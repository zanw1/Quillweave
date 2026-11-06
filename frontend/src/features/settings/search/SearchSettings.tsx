import { useEffect, useState } from "react";
import {
  IconPlus,
  IconSearch,
  IconChevronLeft,
  IconChevronRight,
} from "@tabler/icons-react";
import { Button, Badge, ErrorBox, Switch, Modal } from "../../../components/ui";
import { Select } from "../../../components/Select";
import { message } from "../../../lib/api";
import { t } from "../../../lib/i18n";
import { notifySearchChanged, searchApi } from "../../../lib/search";
import type {
  SearchProfile,
  SearchProfileInfo,
  SearchProfilePage,
} from "../../../domain/search";
import { useSearchRun } from "../../search/useSearchRun";
import { SearchEditor, searchModeNames } from "./SearchEditor";
import { SearchRunView } from "./SearchRunView";
import { searchProfilePayload, useSearchSettings } from "./state";
import "./search.css";

/** Manage actual search profiles, policy, ordered fallbacks and paid tests through the shared backend. */
export function SearchSettings({ embedded = false }: { embedded?: boolean }) {
  const state = useSearchSettings();
  const [records, setRecords] = useState<SearchProfilePage | null>(null);
  const [query, setQuery] = useState("");
  const [family, setFamily] = useState("all");
  const [page, setPage] = useState(1);
  const [editor, setEditor] = useState<SearchProfile | null>(null);
  const [deleting, setDeleting] = useState<SearchProfile | null>(null);
  const [saving, setBusy] = useState(false);
  const [switchPending, setSwitchPending] = useState<boolean | null>(null);
  const [notice, setNotice] = useState("");
  const [policy, setPolicy] = useState<{
    enabled: boolean;
    default_profile_id: string;
    fallback_policy: string;
    fallback_profile_ids: string[];
  } | null>(null);
  const [policyDirty, setPolicyDirty] = useState(false);
  const testing = useSearchRun();
  const [testInfo, setTestInfo] = useState<SearchProfileInfo | null>(null);
  const busy = saving || testing.busy;
  const view = state.view,
    catalog = state.catalog;

  useEffect(() => {
    if (view && !policyDirty)
      setPolicy({
        enabled: view.settings.enabled,
        default_profile_id: view.settings.default_profile_id,
        fallback_policy: view.settings.fallback_policy,
        fallback_profile_ids: view.settings.fallback_profile_ids || [],
      });
  }, [view, policyDirty]);
  useEffect(() => {
    if (!view) return;
    let active = true;
    void searchApi<SearchProfilePage>("page_search_profiles", {
      pageNo: page,
      pageSize: 5,
      query,
      family,
    })
      .then((result) => {
        if (active) {
          setRecords(result);
          if (page > 1 && result.rows.length === 0)
            setPage(Math.max(1, Math.ceil(result.total / 5)));
        }
      })
      .catch((error) => {
        if (active) state.setError(message(error));
      });
    return () => {
      active = false;
    };
  }, [view?.revision, page, query, family]);

  /** Preserve CAS-rejected settings drafts and refresh only after an explicit successful operation. */
  async function perform(operation: string, args: object) {
    setBusy(true);
    state.setError("");
    setNotice("");
    try {
      const result = await state.mutate(operation, args);
      setNotice(t("Search settings saved"));
      return result;
    } catch (error) {
      state.setError(message(error));
      throw error;
    } finally {
      setBusy(false);
    }
  }
  /** Stop all future research immediately, rather than submitting a stale full policy form. */
  async function disable() {
    setBusy(true);
    state.setError("");
    try {
      const next = await searchApi<typeof view>("disable_search");
      state.setView(next);
      setPolicy((current) =>
        current ? { ...current, enabled: false } : current,
      );
      notifySearchChanged();
      setNotice(
        t("Web search disabled. Accepted supplier work may still be billed."),
      );
    } catch (error) {
      state.setError(message(error));
    } finally {
      setBusy(false);
    }
  }
  /** Reflect the requested switch position during persistence and restore the saved state if the operation fails. */
  async function toggle(enabled: boolean) {
    setSwitchPending(enabled);
    try {
      if (!enabled) await disable();
      else {
        const current = view!.settings;
        await perform("set_policy", {
          policy: {
            enabled: true,
            default_profile_id: current.default_profile_id,
            fallback_policy: current.fallback_policy,
            fallback_profile_ids: current.fallback_profile_ids,
          },
        });
        setPolicy((draft) => (draft ? { ...draft, enabled: true } : draft));
      }
    } catch {
      /* The shared error box explains why the saved switch state is restored. */
    } finally {
      setSwitchPending(null);
    }
  }
  /** Save the complete visible policy only after the author requests it. */
  async function savePolicy() {
    if (!policy) return;
    try {
      await perform("set_policy", { policy });
      setPolicyDirty(false);
    } catch {
      /* The visible draft remains for correction or reload. */
    }
  }
  /** Use an explicit reference check and confirmation before deleting persisted connection metadata. */
  async function remove(profile: SearchProfile) {
    try {
      await perform("delete_profile", { profile_id: profile.id });
      setDeleting(null);
    } catch {
      /* Shared error box retains the failure. */
    }
  }
  /** Create a stable fresh ID and unique default name without copying another connection's credentials. */
  function add() {
    if (!catalog || !view) return;
    const profile = structuredClone(
      catalog.presets.find((profile) => profile.family === "deepseek")!,
    );
    profile.id = crypto.randomUUID();
    profile.name = t("DeepSeek search");
    const stem = profile.name;
    let index = 2;
    while (view.settings.profiles.some((item) => item.name === profile.name))
      profile.name = `${stem} ${index++}`;
    setEditor(profile);
  }
  /** Run one explicitly billed saved-connection test and keep its durable result available after failure. */
  async function test(profile: SearchProfile) {
    state.setError("");
    setTestInfo(null);
    const finished = await testing.start({
      purpose: "test",
      operation: "search",
      query: t("Find the official documentation for this search service"),
      selection: { mode: "profile", profile_id: profile.id },
    });
    if (finished)
      try {
        setTestInfo(
          await searchApi<SearchProfileInfo>("get_search_profile", {
            profile_id: profile.id,
          }),
        );
      } catch (error) {
        state.setError(message(error));
      }
  }
  /** Update fallback order without duplicating the default connection or silently enabling a provider. */
  function reorder(index: number) {
    if (!policy || index === 0) return;
    const ids = [...policy.fallback_profile_ids];
    [ids[index - 1], ids[index]] = [ids[index], ids[index - 1]];
    setPolicy({ ...policy, fallback_profile_ids: ids });
    setPolicyDirty(true);
  }

  if (!view || !catalog || !policy)
    return (
      <section className="search-settings">
        <p>{t("Loading search settings…")}</p>
        {state.error && (
          <>
            <ErrorBox>{state.error}</ErrorBox>
            <Button onClick={() => void state.reload().catch(() => {})}>
              {t("Reload")}
            </Button>
          </>
        )}
      </section>
    );
  return (
    <section className="search-settings" aria-label={t("Search settings")}>
      {!embedded && (
        <div className="section-title">
          <div>
            <h2>{t("Search profiles")}</h2>
            <p>
              {t(
                "Choose search independently from writing, then reuse it in discussion and research.",
              )}
            </p>
          </div>
        </div>
      )}
      {state.error && <ErrorBox>{state.error}</ErrorBox>}
      {testing.error && <ErrorBox>{testing.error}</ErrorBox>}
      {notice && (
        <p className="studio-notice" role="status">
          {notice}
        </p>
      )}
      <section className="search-card">
        <div className="search-card-heading">
          <div>
            <h3>{t("Search policy")}</h3>
            <p className="muted">
              {t(
                "The global switch overrides every model and request. New paid connections start disabled.",
              )}
            </p>
          </div>
          <label className="checkbox-label">
            <Switch
              aria-label={t("Enable web search")}
              checked={switchPending ?? view.settings.enabled}
              disabled={saving}
              onChange={(event) => void toggle(event.target.checked)}
            />
            {t("Enable web search")}
          </label>
        </div>
        <div className="form-grid">
          <label>
            {t("Default search profile")}
            <Select
              aria-label={t("Default search profile")}
              value={policy.default_profile_id}
              onChange={(event) => {
                setPolicy({
                  ...policy,
                  default_profile_id: event.target.value,
                  fallback_profile_ids: policy.fallback_profile_ids.filter(
                    (id) => id !== event.target.value,
                  ),
                });
                setPolicyDirty(true);
              }}
            >
              <option value="">{t("Unassigned")}</option>
              {view.settings.profiles.map((profile) => (
                <option key={profile.id} value={profile.id}>
                  {profile.name}
                  {profile.enabled ? "" : ` · ${t("Disabled")}`}
                </option>
              ))}
            </Select>
          </label>
          <label>
            {t("Fallback policy")}
            <Select
              aria-label={t("Fallback policy")}
              value={policy.fallback_policy}
              onChange={(event) => {
                setPolicy({ ...policy, fallback_policy: event.target.value });
                setPolicyDirty(true);
              }}
            >
              <option value="none">{t("Do not switch automatically")}</option>
              <option value="ordered">
                {t("Ordered fallback on transient failures")}
              </option>
              {policy.fallback_policy === "legacy" && (
                <option value="legacy">
                  {t("Preserved legacy fallback rules")}
                </option>
              )}
            </Select>
          </label>
        </div>
        {policy.fallback_policy !== "none" && (
          <>
            <label>
              {t("Add fallback search profile")}
              <Select
                aria-label={t("Add fallback search profile")}
                value=""
                disabled={policy.fallback_profile_ids.length >= 4}
                onChange={(event) => {
                  if (event.target.value) {
                    setPolicy({
                      ...policy,
                      fallback_profile_ids: [
                        ...policy.fallback_profile_ids,
                        event.target.value,
                      ],
                    });
                    setPolicyDirty(true);
                  }
                }}
              >
                <option value="">{t("Choose a saved profile")}</option>
                {view.settings.profiles
                  .filter(
                    (profile) =>
                      profile.id !== policy.default_profile_id &&
                      !policy.fallback_profile_ids.includes(profile.id),
                  )
                  .map((profile) => (
                    <option key={profile.id} value={profile.id}>
                      {profile.name}
                    </option>
                  ))}
              </Select>
            </label>
            <ol className="search-fallback-list">
              {policy.fallback_profile_ids.map((id, index) => (
                <li key={id}>
                  <span>
                    {view.settings.profiles.find((profile) => profile.id === id)
                      ?.name || t("Unavailable profile")}
                  </span>
                  <Button
                    size="sm"
                    disabled={index === 0 || busy}
                    aria-label={t("Move fallback {0} up", index + 1)}
                    onClick={() => reorder(index)}
                  >
                    {t("Move up")}
                  </Button>
                  <Button
                    size="sm"
                    disabled={busy}
                    onClick={() => {
                      setPolicy({
                        ...policy,
                        fallback_profile_ids:
                          policy.fallback_profile_ids.filter(
                            (item) => item !== id,
                          ),
                      });
                      setPolicyDirty(true);
                    }}
                  >
                    {t("Remove")}
                  </Button>
                </li>
              ))}
            </ol>
          </>
        )}
        <div className="search-actions">
          <Button
            primary
            disabled={busy || !policyDirty}
            onClick={() => void savePolicy()}
          >
            {t("Save search policy")}
          </Button>
          <Button
            disabled={busy}
            onClick={() => {
              setPolicyDirty(false);
              void state.reload().catch(() => {});
            }}
          >
            {t("Reload saved policy")}
          </Button>
        </div>
      </section>
      <section className="search-card">
        <div className="search-card-heading">
          <h3>{t("Search connections")}</h3>
          <Button primary disabled={busy} onClick={add}>
            <IconPlus size={16} />
            {t("Add search profile")}
          </Button>
        </div>
        <div className="search-profile-filters">
          <label className="search">
            <IconSearch size={17} />
            <input
              aria-label={t("Filter search profiles")}
              placeholder={t("Search profile name…")}
              value={query}
              onChange={(event) => {
                setQuery(event.target.value);
                setPage(1);
              }}
            />
          </label>
          <label>
            {t("Service")}
            <Select
              aria-label={t("Filter search service")}
              value={family}
              onChange={(event) => {
                setFamily(event.target.value);
                setPage(1);
              }}
            >
              <option value="all">{t("All search services")}</option>
              {catalog.services.map((service) => (
                <option key={service.family} value={service.family}>
                  {t(service.name)}
                </option>
              ))}
            </Select>
          </label>
        </div>
        <table className="search-profile-table">
          <thead>
            <tr>
              <th>{t("Profile")}</th>
              <th>{t("Service / mode")}</th>
              <th>{t("Status")}</th>
              <th>{t("Actions")}</th>
            </tr>
          </thead>
          <tbody>
            {records?.rows.map((profile) => (
              <tr key={profile.id}>
                <td>
                  <strong>{profile.name}</strong>
                  {profile.id === view.settings.default_profile_id && (
                    <Badge>{t("Default")}</Badge>
                  )}
                  <small>
                    {profile.endpoint || t("Endpoint not configured")}
                  </small>
                </td>
                <td>
                  {t(
                    catalog.services.find(
                      (service) => service.family === profile.family,
                    )?.name || profile.family,
                  )}
                  <small>
                    {t(searchModeNames[profile.mode] || profile.mode)}
                  </small>
                </td>
                <td>
                  <Badge tone={profile.enabled ? "green" : "gray"}>
                    {t(profile.enabled ? "Enabled" : "Disabled")}
                  </Badge>
                  <small>
                    {view.key_status[profile.id]
                      ? t("Key configured")
                      : t("No key configured")}
                  </small>
                </td>
                <td>
                  <div className="search-row-actions">
                    <Button
                      size="sm"
                      disabled={busy}
                      onClick={() => setEditor(profile)}
                    >
                      {t("Edit")}
                    </Button>
                    <Button
                      size="sm"
                      disabled={busy || !view.settings.enabled}
                      title={
                        !view.settings.enabled
                          ? t("Enable web search before testing")
                          : t("Testing may incur charges")
                      }
                      onClick={() => void test(profile)}
                    >
                      {t("Test search")}
                    </Button>
                    <Button
                      size="sm"
                      disabled={busy}
                      aria-label={t(
                        'Delete search profile "{0}"',
                        profile.name,
                      )}
                      onClick={() => {
                        state.setError("");
                        setDeleting(profile);
                      }}
                    >
                      {t("Delete")}
                    </Button>
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        {records && !records.total && (
          <p className="muted">{t("No matching search profiles")}</p>
        )}
        <div
          className="search-pagination"
          aria-label={t("Search profile pagination")}
        >
          <span>
            {t(
              "Showing {0}–{1} of {2}",
              records?.total ? (page - 1) * 5 + 1 : 0,
              Math.min(page * 5, records?.total || 0),
              records?.total || 0,
            )}
          </span>
          <div>
            <Button
              size="sm"
              aria-label={t("Previous page")}
              disabled={page === 1 || busy}
              onClick={() => setPage(page - 1)}
            >
              <IconChevronLeft size={15} />
            </Button>
            <span>{page}</span>
            <Button
              size="sm"
              aria-label={t("Next page")}
              disabled={busy || page * 5 >= (records?.total || 0)}
              onClick={() => setPage(page + 1)}
            >
              <IconChevronRight size={15} />
            </Button>
          </div>
        </div>
      </section>
      {testing.run && (
        <SearchRunView
          run={testing.run}
          onCancel={() => void testing.cancel()}
        />
      )}
      {testInfo?.last_test && (
        <p className="muted">
          {testInfo.test_valid
            ? t(
                "The latest test matches this configuration and current process credentials.",
              )
            : t(
                "The latest test is historical or inconclusive. A new process or changed configuration requires testing again.",
              )}
        </p>
      )}
      {editor && (
        <SearchEditor
          key={editor.id}
          initial={editor}
          view={view}
          catalog={catalog}
          onSave={async (profile, secret) => {
            await state.mutate(
              "upsert_profile",
              searchProfilePayload(profile, secret),
            );
            testing.reset();
            setNotice(t("Search profile saved"));
          }}
          onClose={() => setEditor(null)}
        />
      )}
      {deleting && (
        <Modal
          title={t("Delete search profile")}
          onClose={() => {
            if (!saving) setDeleting(null);
          }}
        >
          <div className="modal-content">
            <p>{t('Delete search profile "{0}"?', deleting.name)}</p>
            {state.error && <ErrorBox>{state.error}</ErrorBox>}
          </div>
          <div className="modal-actions">
            <Button disabled={saving} onClick={() => setDeleting(null)}>
              {t("Cancel")}
            </Button>
            <Button
              primary
              disabled={saving}
              onClick={() => void remove(deleting)}
            >
              {t("Delete")}
            </Button>
          </div>
        </Modal>
      )}
    </section>
  );
}
