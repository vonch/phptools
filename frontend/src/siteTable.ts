import type { Site, SiteSortableKey, SiteViewPreferences } from "./types";
import { compareNames } from "./tableSort";

export function defaultSiteView(): SiteViewPreferences {
  return {
    hiddenColumns: [],
    siteOrder: [],
    sortBy: "manual",
    sortDirection: "asc",
  };
}

export function nextSiteSort(
  preferences: SiteViewPreferences,
  key: SiteSortableKey = "name",
): SiteViewPreferences {
  const ascending = preferences.sortBy === key && preferences.sortDirection === "asc";
  const descending = preferences.sortBy === key && preferences.sortDirection === "desc";
  return {
    ...preferences,
    siteOrder: [],
    sortBy: descending ? "manual" : key,
    sortDirection: ascending ? "desc" : "asc",
  };
}

export function orderedSites(
  sites: Site[],
  preferences: SiteViewPreferences,
): Site[] {
  const direction = preferences.sortDirection === "desc" ? -1 : 1;
  // Default order follows successful configuration saves.
  return [...sites].sort((left, right) => {
    if (preferences.sortBy === "createdAt" || preferences.sortBy === "lastActiveAt") {
      const leftTime = Date.parse(left[preferences.sortBy]);
      const rightTime = Date.parse(right[preferences.sortBy]);
      const leftKnown = Number.isFinite(leftTime);
      const rightKnown = Number.isFinite(rightTime);
      if (leftKnown !== rightKnown) return leftKnown ? -1 : 1;
      return leftKnown ? (leftTime - rightTime) * direction : 0;
    }
    if (preferences.sortBy !== "name") {
      const leftTime = Date.parse(left.updatedAt || left.createdAt);
      const rightTime = Date.parse(right.updatedAt || right.createdAt);
      const leftKnown = Number.isFinite(leftTime);
      const rightKnown = Number.isFinite(rightTime);
      if (leftKnown !== rightKnown) return leftKnown ? -1 : 1;
      return leftKnown ? rightTime - leftTime : 0;
    }
    return compareNames(left.name, right.name) * direction;
  });
}


