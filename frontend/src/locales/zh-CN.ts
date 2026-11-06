import { zhBase } from "./zh-base";
import { zhMessages } from "./zh-messages";
import { zhStudio } from "./zh-studio";
import { zhSearch } from "./zh-search";

/** Combine modular interface dictionaries without changing legacy message keys. */
export const zhCN: Record<string, string> = {
  ...zhBase,
  ...zhMessages,
  ...zhStudio,
  ...zhSearch,
};
