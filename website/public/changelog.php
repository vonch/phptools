<?php

declare(strict_types=1);

require dirname(__DIR__) . '/lib/bootstrap.php';
require dirname(__DIR__) . '/lib/release.php';
require dirname(__DIR__) . '/lib/layout.php';

$releases = changelog_releases();
$current = current_release();
$currentVersion = (string) ($current['version'] ?? '');
render_public_start('PHPTools 更新日志', 'changelog');
?>
<section class="download-hero">
    <p class="eyebrow">更新日志</p>
    <h1>每次发布的说明。</h1>
    <p class="lede">这里的内容和后台发布版本时填写的更新说明是同一份。保存新版本后，日志会一起更新，软件检查更新时看到的也是这段说明。</p>
</section>
<section class="changelog-wrap">
    <?php if (!$releases): ?>
        <article class="download-card">
            <h2>还没有更新日志</h2>
            <p>发布版本并填写更新说明后，记录会出现在这里。</p>
        </article>
    <?php else: ?>
        <div class="log-list">
            <?php foreach ($releases as $item): ?>
                <article class="log-item">
                    <header>
                        <h3>
                            <?= h((string) $item['version']) ?>
                            <?= !empty($item['mandatory']) ? ' · 重要更新' : '' ?>
                            <?= ($item['version'] ?? '') === $currentVersion ? ' · 当前版本' : '' ?>
                        </h3>
                        <time><?= h(format_release_time((string) ($item['releaseDate'] ?? ''))) ?></time>
                    </header>
                    <p class="notes"><?= h((string) ($item['notes'] ?? '')) ?></p>
                </article>
            <?php endforeach; ?>
        </div>
    <?php endif; ?>
</section>
<?php
render_public_end();
