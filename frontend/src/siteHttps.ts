export interface HTTPSFormSite {
  id: string;
  https: boolean;
  forceHttps?: boolean;
  port: number | null;
  domain: string;
  secondDomain?: string;
}

export interface HTTPSFormStatus {
  text: string;
  addresses: string[];
}

export function httpsFormStatus(site: HTTPSFormSite, openedHttps: boolean): HTTPSFormStatus {
  const domains = [site.domain, site.secondDomain ?? ""].map((domain) => domain.trim()).filter(Boolean);
  const addresses = domains.map((domain) => `https://${domain}`);
  const names = domains.length > 1 ? "第一域名和第二域名" : "第一域名";
  const port = site.port ?? 0;
  if (site.https && site.id && openedHttps) {
    if (port === 443) {
      return { text: `端口 443 已改为 HTTPS，本地证书包含${names}。`, addresses };
    }
    if (site.forceHttps) {
      return {
        text: `HTTP ${port} 会跳转到 HTTPS 443，本地证书包含${names}。`,
        addresses,
      };
    }
    return {
      text: `HTTP ${port} 保持不变，已额外监听 HTTPS 443，本地证书包含${names}。`,
      addresses,
    };
  }
  if (!site.https && site.id && openedHttps) {
    return { text: "保存后将关闭 HTTPS，不再监听 443。", addresses: [] };
  }
  if (site.https && port === 443) {
    return {
      text: "保存后这个端口改为 HTTPS，并生成本地证书。首次开启时，Windows 会询问是否信任本环境的证书颁发机构。",
      addresses,
    };
  }
  if (site.https) {
    return {
      text: site.forceHttps
        ? `保存后 HTTP ${port} 会跳转到 HTTPS 443。首次开启时，Windows 会询问是否信任本环境的证书颁发机构。`
        : "保存后为填写的域名生成证书，并增加 443 端口。原来的 HTTP 端口仍可访问。首次开启时，Windows 会询问是否信任本环境的证书颁发机构。",
      addresses,
    };
  }
  if (port === 443) {
    return {
      text: "开启后这个端口改为 HTTPS，并生成本地证书。首次开启时，Windows 会询问是否信任本环境的证书颁发机构。",
      addresses: [],
    };
  }
  return {
    text: "开启后为填写的域名生成证书，并增加 443 端口。原来的 HTTP 端口仍可访问。首次开启时，Windows 会询问是否信任本环境的证书颁发机构。",
    addresses: [],
  };
}
