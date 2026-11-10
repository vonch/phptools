<?php

declare(strict_types=1);

require dirname(__DIR__) . '/lib/bootstrap.php';
require dirname(__DIR__) . '/lib/release.php';
require dirname(__DIR__) . '/lib/layout.php';

$release = current_release();
render_public_start('下载 PHPTools', 'download');
?>
<section class="download-hero">
    <p class="eyebrow">Windows x64</p>
    <h1>下载 PHPTools。</h1>
    <p class="lede">安装包里只有管理程序。Nginx、PHP、MySQL 和 Redis 在软件里按需下载。软件检查更新时读取的也是这一份 EXE。</p>
</section>
<?php if (!$release): ?>
    <section class="download-card">
        <h2>还没有发布版本</h2>
        <p>版本发布后，这里会显示版本号、文件大小、发布时间和 SHA-256。</p>
    </section>
<?php else: ?>
    <section class="download-card">
        <div class="download-top">
            <div>
                <p class="eyebrow">当前版本</p>
                <h2><?= h($release['version']) ?><?= !empty($release['mandatory']) ? ' · 重要更新' : '' ?></h2>
            </div>
            <a class="button" href="/PHPTools.exe">下载 PHPTools.exe</a>
        </div>
        <div class="meta">
            <div><span>发布时间</span><strong><?= h(format_release_time((string) $release['releaseDate'])) ?></strong></div>
            <div><span>大小</span><strong><?= h(format_bytes((int) $release['size'])) ?></strong></div>
            <div><span>系统</span><strong>Windows 10/11 x64</strong></div>
        </div>
        <p class="hash"><span class="label">SHA-256</span><code><?= h((string) $release['sha256']) ?></code></p>
        <?php if (($release['notes'] ?? '') !== ''): ?>
            <div class="notes-block">
                <span class="label">更新说明</span>
                <p class="notes"><?= h((string) $release['notes']) ?></p>
            </div>
        <?php endif; ?>
    </section>
<?php endif; ?>
<section class="split">
    <article class="sheet">
        <p class="eyebrow">运行条件</p>
        <h2>打开之前先确认这三样。</h2>
        <ul class="plain-list">
            <li>Windows 10 或 Windows 11，64 位。</li>
            <li>已安装 Microsoft Edge WebView2。没有的话，软件会打开微软的安装页面。</li>
            <li>准备一个空目录作为环境目录，不要放进其他环境的安装目录，也不要放在磁盘根目录。</li>
        </ul>
    </article>
    <article class="sheet">
        <p class="eyebrow">拿到的是什么</p>
        <h2>一份程序，一份你自己的环境。</h2>
        <ul class="plain-list">
            <li>下载的是 PHPTools.exe。软件内更新也只接受这个 EXE，不接受 ZIP。</li>
            <li>第一次运行时选择空目录，程序会生成 metadata.db 以及组件、站点、日志目录。</li>
            <li>数据库密码记在当前 Windows 用户的凭据管理器，不写进这个 sqlite 文件。</li>
        </ul>
    </article>
</section>
<?php
render_public_end();
