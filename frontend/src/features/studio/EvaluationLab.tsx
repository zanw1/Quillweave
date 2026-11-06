import { useEffect, useState } from "react";
import { t } from "../../lib/i18n";
import { api, message } from "../../lib/api";
import { Badge, Button, ErrorBox } from "../../components/ui";
import { Select } from "../../components/Select";
import type { Job, Project } from "../../domain/types";
import type { PolicyEvaluation, PolicyExperiment } from "../../domain/studio";

/** Initialize an empty evaluation with visible baseline and candidate policy budgets. */
function newExperiment(): PolicyExperiment {
  return {
    id: "",
    title: "",
    created_at: "",
    cost_penalty: 0.01,
    min_gain: 0.02,
    policies: [
      { id: "baseline", order: "breadth", max_calls: 4, max_depth: 3 },
      { id: "candidate", order: "best_parent", max_calls: 4, max_depth: 3 },
    ],
    cases: [],
  };
}

/** Compare replay strategies using author-scored real candidates and an untouched held-out split. */
export function EvaluationLab({ project }: { project: Project }) {
  const [draft, setDraft] = useState(newExperiment);
  const [saved, setSaved] = useState<PolicyExperiment[]>([]);
  const [runs, setRuns] = useState<Job[]>([]);
  const [jobId, setJobId] = useState("");
  const [caseId, setCaseId] = useState("");
  const [evaluation, setEvaluation] = useState<PolicyEvaluation | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState("");
  useEffect(() => {
    void api<PolicyExperiment[]>("list_experiments", { project_id: project.id })
      .then(setSaved)
      .catch((error) => setError(message(error)));
    void api<Job[]>("get_runs", { project_id: project.id })
      .then((items) =>
        setRuns(items.filter((item) => item.status === "done" && item.text)),
      )
      .catch(() => {});
  }, [project.id]);

  /** Add a named frozen case before assigning it to training or held-out evaluation. */
  function addCase() {
    const id = crypto.randomUUID();
    setDraft({
      ...draft,
      cases: [...draft.cases, { id, split: "train", nodes: [] }],
    });
    setCaseId(id);
    setEvaluation(null);
  }

  /** Import an actual completed draft as an unscored candidate, retaining its text and task identity. */
  function addNode() {
    const run = runs.find((item) => item.id === jobId);
    if (!run) return;
    setDraft({
      ...draft,
      cases: draft.cases.map((item) =>
        item.id === caseId
          ? {
              ...item,
              nodes: [
                ...item.nodes,
                {
                  id: crypto.randomUUID(),
                  parent_id: "",
                  score: 0,
                  cost: run.calls || 1,
                  text: run.text.slice(0, 12000),
                  reviewed: false,
                },
              ],
            }
          : item,
      ),
    });
    setEvaluation(null);
  }

  /** Evaluate with fixed recorded outcomes; only explicit save writes the research record. */
  async function run(save: boolean) {
    setBusy(true);
    setError("");
    try {
      const result = await api<{
        experiment: PolicyExperiment;
        evaluation: PolicyEvaluation;
      }>(save ? "record_experiment" : "evaluate_policy", {
        project_id: project.id,
        experiment: draft,
      });
      setEvaluation(result.evaluation);
      if (save) {
        setSaved([result.experiment, ...saved]);
        setNotice(t("Saved"));
      }
    } catch (error) {
      setError(message(error));
    } finally {
      setBusy(false);
    }
  }

  const current = draft.cases.find((item) => item.id === caseId);
  return (
    <section className="studio-section evaluation-lab">
      <h2>{t("Policy experiments")}</h2>
      <p className="muted">
        {t(
          "Replay uses recorded branches only. Scores must come from author review. No model weights or production prompts are changed.",
        )}
      </p>
      <div className="studio-form">
        <label>
          {t("Title")}
          <input
            value={draft.title}
            onChange={(event) =>
              setDraft({ ...draft, title: event.target.value })
            }
          />
        </label>
        <div className="studio-actions">
          <Button onClick={addCase}>{t("Add evaluation case")}</Button>
          <Select
            aria-label={t("Load experiment")}
            value=""
            onChange={(event) => {
              const next = saved.find((item) => item.id === event.target.value);
              if (next) {
                setDraft(next);
                setCaseId(next.cases[0]?.id || "");
                setEvaluation(null);
              }
            }}
          >
            <option value="">{t("Load experiment")}</option>
            {saved.map((item) => (
              <option value={item.id} key={item.id}>
                {item.title || item.created_at}
              </option>
            ))}
          </Select>
        </div>
        <div className="policy-grid">
          {draft.policies.map((policy, index) => (
            <fieldset key={policy.id}>
              <legend>{policy.id}</legend>
              <label>
                {t("Exploration order")}
                <Select
                  value={policy.order}
                  onChange={(event) =>
                    setDraft({
                      ...draft,
                      policies: draft.policies.map((item, i) =>
                        i === index
                          ? { ...item, order: event.target.value }
                          : item,
                      ),
                    })
                  }
                >
                  <option value="breadth">{t("Explore broadly")}</option>
                  <option value="depth">{t("Refine one direction")}</option>
                  <option value="best_parent">
                    {t("Prioritize promising parents")}
                  </option>
                </Select>
              </label>
              <label>
                {t("Model calls")}
                <input
                  type="number"
                  min={1}
                  max={100}
                  value={policy.max_calls}
                  onChange={(event) =>
                    setDraft({
                      ...draft,
                      policies: draft.policies.map((item, i) =>
                        i === index
                          ? { ...item, max_calls: Number(event.target.value) }
                          : item,
                      ),
                    })
                  }
                />
              </label>
              <label>
                {t("Maximum depth")}
                <input
                  type="number"
                  min={1}
                  max={100}
                  value={policy.max_depth}
                  onChange={(event) =>
                    setDraft({
                      ...draft,
                      policies: draft.policies.map((item, i) =>
                        i === index
                          ? { ...item, max_depth: Number(event.target.value) }
                          : item,
                      ),
                    })
                  }
                />
              </label>
            </fieldset>
          ))}
        </div>
        <div className="form-grid">
          <label>
            {t("Evaluation case")}
            <Select
              value={caseId}
              onChange={(event) => setCaseId(event.target.value)}
            >
              <option value="">{t("Choose an option")}</option>
              {draft.cases.map((item, index) => (
                <option key={item.id} value={item.id}>
                  {index + 1} ·{" "}
                  {t(item.split === "train" ? "Training" : "Held out")}
                </option>
              ))}
            </Select>
          </label>
          {current && (
            <label>
              {t("Evaluation split")}
              <Select
                value={current.split}
                onChange={(event) =>
                  setDraft({
                    ...draft,
                    cases: draft.cases.map((item) =>
                      item.id === caseId
                        ? { ...item, split: event.target.value }
                        : item,
                    ),
                  })
                }
              >
                <option value="train">{t("Training")}</option>
                <option value="holdout">{t("Held out")}</option>
              </Select>
            </label>
          )}
        </div>
        {current && (
          <>
            <div className="studio-actions">
              <Select
                aria-label={t("Completed candidate")}
                value={jobId}
                onChange={(event) => setJobId(event.target.value)}
              >
                <option value="">{t("Completed candidate")}</option>
                {runs.map((item) => (
                  <option value={item.id} key={item.id}>
                    {item.instruction.slice(0, 70)}
                  </option>
                ))}
              </Select>
              <Button disabled={!jobId} onClick={addNode}>
                {t("Add candidate")}
              </Button>
            </div>
            {current.nodes.map((node, index) => (
              <article className="research-card" key={node.id}>
                <details>
                  <summary>
                    {t("Candidate")} {index + 1}
                  </summary>
                  <pre>{node.text}</pre>
                </details>
                <div className="form-grid">
                  <label>
                    {t("Parent candidate")}
                    <Select
                      value={node.parent_id}
                      onChange={(event) =>
                        setDraft({
                          ...draft,
                          cases: draft.cases.map((item) =>
                            item.id === caseId
                              ? {
                                  ...item,
                                  nodes: item.nodes.map((value) =>
                                    value.id === node.id
                                      ? {
                                          ...value,
                                          parent_id: event.target.value,
                                        }
                                      : value,
                                  ),
                                }
                              : item,
                          ),
                        })
                      }
                    >
                      <option value="">{t("No parent")}</option>
                      {current.nodes
                        .filter((item) => item.id !== node.id)
                        .map((item) => (
                          <option value={item.id} key={item.id}>
                            {current.nodes.indexOf(item) + 1}
                          </option>
                        ))}
                    </Select>
                  </label>
                  <label>
                    {t("Quality")} (0–5)
                    <input
                      type="number"
                      min={0}
                      max={5}
                      step={0.5}
                      value={node.score * 5}
                      onChange={(event) =>
                        setDraft({
                          ...draft,
                          cases: draft.cases.map((item) =>
                            item.id === caseId
                              ? {
                                  ...item,
                                  nodes: item.nodes.map((value) =>
                                    value.id === node.id
                                      ? {
                                          ...value,
                                          score: Number(event.target.value) / 5,
                                          reviewed: true,
                                        }
                                      : value,
                                  ),
                                }
                              : item,
                          ),
                        })
                      }
                    />
                  </label>
                  <label>
                    {t("Cost")}
                    <input
                      type="number"
                      min={0}
                      step={0.01}
                      value={node.cost}
                      onChange={(event) =>
                        setDraft({
                          ...draft,
                          cases: draft.cases.map((item) =>
                            item.id === caseId
                              ? {
                                  ...item,
                                  nodes: item.nodes.map((value) =>
                                    value.id === node.id
                                      ? {
                                          ...value,
                                          cost: Number(event.target.value),
                                        }
                                      : value,
                                  ),
                                }
                              : item,
                          ),
                        })
                      }
                    />
                  </label>
                </div>
                <label className="checkbox-label">
                  <input
                    type="checkbox"
                    checked={!!node.reviewed}
                    onChange={(event) =>
                      setDraft({
                        ...draft,
                        cases: draft.cases.map((item) =>
                          item.id === caseId
                            ? {
                                ...item,
                                nodes: item.nodes.map((value) =>
                                  value.id === node.id
                                    ? {
                                        ...value,
                                        reviewed: event.target.checked,
                                      }
                                    : value,
                                ),
                              }
                            : item,
                        ),
                      })
                    }
                  />
                  {t("Author score confirmed")}
                </label>
              </article>
            ))}
          </>
        )}
      </div>
      <div className="studio-actions">
        <Button
          primary
          disabled={busy || !draft.cases.length}
          onClick={() => void run(false)}
        >
          {t("Evaluate replay")}
        </Button>
        <Button
          disabled={busy || !draft.cases.length}
          onClick={() => void run(true)}
        >
          {t("Save experiment")}
        </Button>
      </div>
      {notice && <p role="status">{notice}</p>}
      {error && <ErrorBox>{error}</ErrorBox>}
      {evaluation && (
        <div className="evaluation-result">
          <Badge tone={evaluation.promotable ? "green" : "amber"}>
            {t(
              evaluation.promotable
                ? "Evidence supports author review"
                : "Insufficient promotion evidence",
            )}
          </Badge>
          <p>
            {t("Candidate")}: {evaluation.candidate}
          </p>
          <p>{t(evaluation.reason)}</p>
          <div className="table-scroll">
            <table>
              <thead>
                <tr>
                  {[
                    "Candidate",
                    "Evaluation split",
                    "Quality",
                    "Cost",
                    "Reward",
                  ].map((title) => (
                    <th key={title}>{t(title)}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {evaluation.scores.map((score) => (
                  <tr key={`${score.policy_id}-${score.split}`}>
                    <td>{score.policy_id}</td>
                    <td>
                      {t(score.split === "train" ? "Training" : "Held out")} (
                      {score.cases})
                    </td>
                    <td>{score.quality.toFixed(3)}</td>
                    <td>{score.cost.toFixed(3)}</td>
                    <td>{score.reward.toFixed(3)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </section>
  );
}
