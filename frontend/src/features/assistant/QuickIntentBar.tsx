import {
  IconBulb,
  IconCheck,
  IconDatabaseImport,
  IconPencil,
  IconSitemap,
  IconSparkles,
  IconUsers,
  IconX,
} from "@tabler/icons-react";
import { getLanguage, t } from "../../lib/i18n";

interface Props {
  intentHint: string;
  disabled?: boolean;
  hasChapter?: boolean;
  onSelectIntent: (hint: string, defaultPrompt?: string) => void;
}

const intentChips = [
  {
    key: "brainstorm",
    label: "Brainstorm directions",
    icon: IconBulb,
    zhPrompt:
      "从当前核心创意出发，帮我比较 2～3 种不同故事方向，分析人物欲望、冲突与长线代价。",
    enPrompt:
      "Compare 2–3 distinct story directions from the current idea, covering character desires, conflicts, and trade-offs.",
  },
  {
    key: "naming",
    label: "Titles & character names",
    icon: IconSparkles,
    zhPrompt:
      "请结合本作品创意提供至少 5 个有区分度的书名和 5 个主角名字，说明寓意与题材契合度。",
    enPrompt:
      "Suggest at least five distinctive book titles and five protagonist names, explaining their meaning and tone fit.",
  },
  {
    key: "world_check",
    label: "Characters & world check",
    icon: IconUsers,
    zhPrompt:
      "帮我梳理核心角色的性格矛盾、人物关系张力，以及世界观规则的限制与代价。",
    enPrompt:
      "Help refine core character flaws, relationship tension, and the costs and limits of world rules.",
  },
  {
    key: "outline_breakdown",
    label: "Volume & chapter outline",
    icon: IconSitemap,
    zhPrompt:
      "将当前讨论的剧情拆解为分卷与章节细纲，明确每章视角、目标、阻碍、选择与后果。",
    enPrompt:
      "Break down our discussed plot into volume and chapter outlines with POV, goal, obstacle, choice, and consequence.",
  },
  {
    key: "prose_polish",
    label: "Prose continuation & polish",
    icon: IconPencil,
    zhPrompt: "围绕当前场景续写或润色正文，强化人物动作细节与情绪张力。",
    enPrompt:
      "Continue or polish the current scene with concrete character actions and emotional tension.",
  },
  {
    key: "commit_extract",
    label: "Extract & commit settings",
    icon: IconDatabaseImport,
    zhPrompt: "把这些讨论好的设定写进创作目标和设定库里面。",
    enPrompt:
      "Extract the settled ideas from our discussion and commit them to the studio goals and story bible.",
  },
];

/** Provide one-shot intent hint chips without locking the assistant out of unified agent mode. */
export function QuickIntentBar({
  intentHint,
  disabled,
  onSelectIntent,
}: Props) {
  const isZh = getLanguage() === "zh-CN";
  return (
    <div
      className="quick-intent-bar"
      role="group"
      aria-label={t("Quick intent hints")}
    >
      {intentChips.map((chip) => {
        const active = intentHint === chip.key;
        const Icon = chip.icon;
        return (
          <button
            key={chip.key}
            type="button"
            disabled={disabled}
            aria-pressed={active}
            className={`intent-chip ${active ? "active" : ""}`}
            onClick={() =>
              onSelectIntent(
                active ? "" : chip.key,
                active ? undefined : isZh ? chip.zhPrompt : chip.enPrompt,
              )
            }
          >
            {active ? <IconCheck size={14} /> : <Icon size={14} />}
            <span>{t(chip.label)}</span>
            {active && <IconX size={13} />}
          </button>
        );
      })}
    </div>
  );
}
