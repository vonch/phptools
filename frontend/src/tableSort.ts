const nameCollator = new Intl.Collator("zh-CN", {
  numeric: true,
  sensitivity: "base",
});

export const compareNames = nameCollator.compare;
