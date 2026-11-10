import type { Kind } from "./types";

const kindOrder: Record<Kind, number> = {
  nginx: 0,
  mysql: 1,
  redis: 2,
  php: 3,
};

function versionParts(value: string): number[] {
  const match = value.match(/\d+(?:\.\d+)*/);
  if (!match) return [];
  return match[0].split(".").map((part) => Number(part) || 0);
}

export function compareServiceInstances(
  left: { kind: Kind; name: string; version?: string },
  right: { kind: Kind; name: string; version?: string },
): number {
  const byKind = (kindOrder[left.kind] ?? 9) - (kindOrder[right.kind] ?? 9);
  if (byKind !== 0) return byKind;
  const leftVersion = versionParts(left.version || left.name);
  const rightVersion = versionParts(right.version || right.name);
  const length = Math.max(leftVersion.length, rightVersion.length);
  for (let index = 0; index < length; index += 1) {
    const delta = (leftVersion[index] ?? 0) - (rightVersion[index] ?? 0);
    if (delta !== 0) return delta;
  }
  return left.name.localeCompare(right.name, "zh-CN");
}
