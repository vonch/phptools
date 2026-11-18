export function runtimeDownloadOffer(error: string): "" | "vc14" {
  return error.includes("系统缺少 Visual C++ 运行库") ? "vc14" : "";
}
