import type { CertificateSortableKey, CertificateViewPreferences, SiteCertificate } from "./types";
import { compareNames } from "./tableSort";

export const certificateKindName: Record<SiteCertificate["kind"], string> = {
  none: "未申请",
  local: "本地证书",
  public: "公网证书",
  invalid: "证书无效",
};

const kindRank: Record<SiteCertificate["kind"], number> = {
  none: 0,
  local: 1,
  public: 2,
  invalid: 3,
};

export function defaultCertificateView(): CertificateViewPreferences {
  return {
    hiddenColumns: [],
    sortBy: "manual",
    sortDirection: "asc",
  };
}

export function nextCertificateSort(
  preferences: CertificateViewPreferences,
  key: CertificateSortableKey = "siteName",
): CertificateViewPreferences {
  const ascending = preferences.sortBy === key && preferences.sortDirection === "asc";
  const descending = preferences.sortBy === key && preferences.sortDirection === "desc";
  return {
    ...preferences,
    sortBy: descending ? "manual" : key,
    sortDirection: ascending ? "desc" : "asc",
  };
}

export function orderedCertificates(
  rows: SiteCertificate[],
  preferences: CertificateViewPreferences,
  manualIds: string[] = [],
): SiteCertificate[] {
  const direction = preferences.sortDirection === "desc" ? -1 : 1;
  return [...rows].sort((left, right) => {
    if (preferences.sortBy === "siteName") return compareNames(left.siteName, right.siteName) * direction;
    if (preferences.sortBy === "domains") return compareText(left.domains.join(" "), right.domains.join(" "), direction);
    if (preferences.sortBy === "kind") return (kindRank[left.kind] - kindRank[right.kind]) * direction;
    if (preferences.sortBy === "notAfter") return compareTime(left.notAfter, right.notAfter, direction);
    return manualIndex(manualIds, left.siteId) - manualIndex(manualIds, right.siteId);
  });
}

export function matchingCertificates(rows: SiteCertificate[], query: string): SiteCertificate[] {
  const term = query.trim().toLowerCase();
  if (!term) return rows;
  return rows.filter((row) =>
    [row.siteName, row.domains.join(" "), certificateKindName[row.kind], row.issuer, row.reason]
      .join(" ")
      .toLowerCase()
      .includes(term),
  );
}

function manualIndex(ids: string[], siteId: string) {
  const index = ids.indexOf(siteId);
  return index < 0 ? Number.MAX_SAFE_INTEGER : index;
}

function compareText(left: string, right: string, direction: number) {
  const leftText = left.trim();
  const rightText = right.trim();
  if (!leftText || !rightText) {
    if (leftText === rightText) return 0;
    return leftText ? -1 : 1;
  }
  return compareNames(leftText, rightText) * direction;
}

function compareTime(left: string, right: string, direction: number) {
  const leftTime = Date.parse(left);
  const rightTime = Date.parse(right);
  const leftKnown = Number.isFinite(leftTime);
  const rightKnown = Number.isFinite(rightTime);
  if (leftKnown !== rightKnown) return leftKnown ? -1 : 1;
  return leftKnown ? (leftTime - rightTime) * direction : 0;
}
