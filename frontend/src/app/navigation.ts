import { IconBook2, IconFolder, IconPencil } from "@tabler/icons-react";
import type { Page } from "../domain/types";

export const navigation = [
  { page: "write" as Page, label: "Chapters", icon: IconPencil },
  { page: "plan" as Page, label: "Story planning", icon: IconBook2 },
  { page: "wiki" as Page, label: "Character Wiki", icon: IconBook2 },
  { page: "knowledge" as Page, label: "Library", icon: IconFolder },
];
export const pageNames: Record<Page, string> = {
  home: "My projects",
  write: "Chapters",
  plan: "Story planning",
  wiki: "Character Wiki",
  knowledge: "Library",
  methods: "Writing methods",
  settings: "Model settings",
};
