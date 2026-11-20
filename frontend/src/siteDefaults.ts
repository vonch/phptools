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

export const maxBatchSites = 50;

export interface BatchSiteDraft {
  name: string;
  domain: string;
  projectFolder: string;
}

export function parseBatchSites(text: string): BatchSiteDraft[] {
  const drafts: BatchSiteDraft[] = [];
  const seen = new Map<string, number>();
  const lines = text.split(/\r?\n/);
  for (let index = 0; index < lines.length; index++) {
    const line = lines[index].trim();
    if (!line) continue;
    const row = index + 1;
    const parts = line.split(/\s+/);
    let name = "";
    let domain = "";
    if (parts.length === 1 && parts[0].includes(".")) {
      domain = parts[0];
      name = parts[0];
    } else if (parts.length === 1) {
      name = parts[0];
      domain = domainFromName(name);
    } else if (parts.length === 2) {
      name = parts[0];
      domain = parts[1];
    } else {
      throw new Error(`第 ${row} 行只能写名称，或写成「名称 域名」`);
    }
    if (!domain) throw new Error(`第 ${row} 行无法生成域名，请写成「名称 域名」`);
    const key = domain.toLowerCase();
    const previous = seen.get(key);
    if (previous) throw new Error(`第 ${row} 行的域名 ${domain} 与第 ${previous} 行重复`);
    seen.set(key, row);
    const folderSource = parts.length === 1 && parts[0].includes(".")
      ? domain.slice(0, domain.indexOf("."))
      : name;
    drafts.push({
      name,
      domain,
      projectFolder: folderFromName(folderSource),
    });
    if (drafts.length > maxBatchSites) throw new Error(`一次最多创建 ${maxBatchSites} 个站点`);
  }
  if (!drafts.length) throw new Error("请至少填写一个站点");
  return drafts;
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
