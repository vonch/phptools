import { pinyin } from "pinyin-pro";

export function domainFromName(name: string): string {
  const converted = pinyin(name.trim(), {
    toneType: "none",
    nonZh: "consecutive",
    v: true,
    separator: "",
  });
  const label = converted
    .toLowerCase()
    .replace(/\s+/g, "-")
    .replace(/[^a-z0-9-]/g, "")
    .replace(/-+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 63)
    .replace(/-+$/g, "");
  return label ? `${label}.test` : "";
}

export function folderFromName(name: string): string {
  const converted = pinyin(name.trim(), {
    toneType: "none",
    nonZh: "consecutive",
    v: true,
    separator: "",
  });
  return converted.toLowerCase().replace(/[^a-z0-9]/g, "").slice(0, 60);
}

export function databaseNameFromSite(name: string): string {
  const converted = pinyin(name.trim(), {
    toneType: "none",
    nonZh: "consecutive",
    v: true,
    separator: "",
  });
  return converted.replace(/[^A-Za-z0-9_]/g, "").slice(0, 32);
}
