<?php

declare(strict_types=1);

function render_public_start(string $title, string $active): void
{
    $release = current_release();
    $description = 'PHPTools 是 Windows 上的本地 PHP 开发环境管理器，用来管理 Nginx、PHP、MySQL 和 Redis。';
    $featureHref = $active === 'home' ? '#capabilities' : '/#capabilities';
    ?>
<!DOCTYPE html>
<html lang="zh-CN">
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title><?= h($title) ?></title>
    <meta name="description" content="<?= h($description) ?>">
    <link rel="icon" href="/assets/favicon.svg" type="image/svg+xml">
    <link rel="stylesheet" href="/assets/site.css?v=20261107a">
</head>
<body class="site">
<header class="topbar">
    <a class="brand" href="/">
        <span class="mark">PHP</span>
        <span>PHPTools</span>
    </a>
    <nav>
        <a class="<?= $active === 'home' ? 'active' : '' ?>" href="/">介绍</a>
        <a href="<?= h($featureHref) ?>">功能</a>
        <a class="<?= $active === 'changelog' ? 'active' : '' ?>" href="/changelog.php">更新日志</a>
        <a class="<?= $active === 'download' ? 'active' : '' ?>" href="/download.php">下载</a>
        <a href="https://github.com/vonch/phptools/issues/new/choose" target="_blank" rel="noreferrer">反馈</a>
    </nav>
    <?php if ($release): ?>
        <a class="button button-small" href="/PHPTools.exe">下载 <?= h($release['version']) ?></a>
    <?php else: ?>
        <a class="button button-small button-quiet" href="/download.php">下载</a>
    <?php endif; ?>
</header>
<main>
    <?php
}

function render_public_end(): void
{
    ?>
</main>
<footer class="site-footer">
    <div class="footer-brand">
        <a class="brand" href="/">
            <span class="mark">PHP</span>
            <span>PHPTools</span>
        </a>
        <p>Windows 上的本地 PHP 开发环境。站点、数据库和密码留在你自己的电脑上。</p>
    </div>
    <div class="footer-col">
        <span>页面</span>
        <a href="/">介绍</a>
        <a href="/#capabilities">功能</a>
        <a href="/changelog.php">更新日志</a>
        <a href="/download.php">下载</a>
        <a href="https://github.com/vonch/phptools/issues/new/choose" target="_blank" rel="noreferrer">反馈</a>
    </div>
    <div class="footer-col">
        <span>运行环境</span>
        <p>Windows 10 / 11 x64</p>
        <p>Microsoft Edge WebView2</p>
    </div>
</footer>
</body>
</html>
    <?php
}

function render_admin_start(string $title): void
{
    $flash = take_flash();
    ?>
<!DOCTYPE html>
<html lang="zh-CN">
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title><?= h($title) ?> · PHPTools</title>
    <link rel="icon" href="/assets/favicon.svg" type="image/svg+xml">
    <link rel="stylesheet" href="/assets/site.css?v=20261107a">
</head>
<body class="admin-body">
<header class="topbar">
    <a class="brand" href="/admin/">
        <span class="mark">PHP</span>
        <span>版本发布</span>
    </a>
    <nav>
        <a href="/admin/">当前版本</a>
        <a href="/admin/publish.php">发布</a>
        <a href="/admin/feedback.php">反馈</a>
        <a href="/" target="_blank" rel="noreferrer">查看官网</a>
    </nav>
    <?php if (admin_logged_in()): ?>
        <a class="button button-small button-quiet" href="/admin/logout.php">退出</a>
    <?php endif; ?>
</header>
<main class="admin-main">
    <?php if ($flash): ?>
        <p class="notice notice-<?= h((string) ($flash['type'] ?? 'info')) ?>"><?= h((string) ($flash['message'] ?? '')) ?></p>
    <?php endif; ?>
    <?php
}

function render_admin_end(): void
{
    ?>
</main>
</body>
</html>
    <?php
}
