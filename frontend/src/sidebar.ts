import { h } from "vue";
import { NIcon } from "naive-ui";
import {
  AppsOutline, CubeOutline, ServerOutline, GlobeOutline,
  GridOutline, ListOutline, SettingsOutline, PulseOutline,
} from "@vicons/ionicons5";
import type { Page, SidebarItem } from "./types";

const icon = (component: typeof GridOutline) => () =>
  h(NIcon, null, { default: () => h(component) });

export const pageCatalog = [
  { key: "overview", label: "概览", icon: icon(GridOutline) },
  { key: "sites", label: "站点", icon: icon(GlobeOutline) },
  { key: "databases", label: "数据库", icon: icon(ServerOutline) },
  { key: "packages", label: "组件中心", icon: icon(CubeOutline) },
  { key: "instances", label: "服务实例", icon: icon(AppsOutline) },
  { key: "health", label: "检测", icon: icon(PulseOutline) },
  { key: "logs", label: "日志", icon: icon(ListOutline) },
  { key: "settings", label: "设置", icon: icon(SettingsOutline) },
] satisfies { key: Page; label: string; icon: () => unknown }[];

export function defaultSidebar(): SidebarItem[] {
  return pageCatalog.map(({ key }) => ({ key, hidden: false, label: "" }));
}

export function pageInfo(key: Page) {
  return pageCatalog.find((page) => page.key === key)!;
}

export function sidebarLabel(item: Pick<SidebarItem, "key" | "label">) {
  const label = item.label?.trim();
  return label || pageInfo(item.key).label;
}
