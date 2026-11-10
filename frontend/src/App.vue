<script setup lang="ts">
import {
  NConfigProvider,
  NDialogProvider,
  NMessageProvider,
  zhCN,
  dateZhCN,
  type GlobalThemeOverrides,
} from "naive-ui";
import Shell from "./Shell.vue";
const theme: GlobalThemeOverrides = {
  common: {
    primaryColor: "#187868",
    primaryColorHover: "#248d7b",
    primaryColorPressed: "#116456",
    borderRadius: "7px",
    fontFamily: 'Inter, "Segoe UI", "Microsoft YaHei", sans-serif',
    fontSize: "13px",
  },
  Button: { fontWeight: "500" },
  Card: { borderRadius: "10px", titleFontSizeMedium: "15px" },
  DataTable: {
    thColor: "#f7f9fa",
    tdColor: "#ffffff",
    tdColorHover: "#f7faf9",
    thTextColor: "#687380",
    thFontWeight: "500",
    borderColor: "#edf0f2",
  },
};
</script>

<template>
  <NConfigProvider
    :theme-overrides="theme"
    :locale="zhCN"
    :date-locale="dateZhCN"
  >
    <NMessageProvider placement="top-right"
      ><NDialogProvider><Shell /></NDialogProvider
    ></NMessageProvider>
  </NConfigProvider>
</template>
