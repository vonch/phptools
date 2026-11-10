<?php

declare(strict_types=1);

require dirname(__DIR__, 2) . '/lib/bootstrap.php';
require dirname(__DIR__, 2) . '/lib/release.php';
require dirname(__DIR__, 2) . '/lib/auth.php';
require dirname(__DIR__, 2) . '/lib/layout.php';

site_session();
require_setup_or_login();

if ($_SERVER['REQUEST_METHOD'] === 'POST') {
    csrf_check();
    try {
        $message = activate_release(trim((string) ($_POST['version'] ?? '')));
        flash($message);
    } catch (RuntimeException $exception) {
        flash($exception->getMessage(), 'error');
    }
    redirect('/admin/');
}

$catalog = load_catalog();
$current = current_release();
$downloadStats = load_download_stats();
render_admin_start('当前版本');
?>
<section class="sheet">
    <p class="eyebrow">正在提供给软件</p>
    <?php if (!$current): ?>
        <h1>还没有发布版本。</h1>
        <p>上传一次 PHPTools.exe 后，网站会写好更新清单和下载文件。</p>
        <div class="hero-actions"><a class="button" href="/admin/publish.php">发布第一个版本</a></div>
    <?php else: ?>
        <h1><?= h($current['version']) ?></h1>
        <div class="meta">
            <div><span>发布时间</span><strong><?= h(format_release_time((string) $current['releaseDate'])) ?></strong></div>
            <div><span>大小</span><strong><?= h(format_bytes((int) $current['size'])) ?></strong></div>
            <div><span>标记</span><strong><?= !empty($current['mandatory']) ? '重要更新' : '普通更新' ?></strong></div>
        </div>
        <p class="hash"><span class="label">更新清单</span><code><?= h(manifest_url()) ?></code></p>
        <p class="hash"><span class="label">程序地址</span><code><?= h(download_url()) ?></code></p>
        <p class="hash"><span class="label">SHA-256</span><code><?= h((string) $current['sha256']) ?></code></p>
        <?php $currentCount = $downloadStats[(string) $current['version']] ?? ['web' => 0, 'update' => 0, 'total' => 0]; ?>
        <div class="download-count">
            <span>下载次数</span>
            <strong><?= number_format($currentCount['total']) ?></strong>
            <p>官网 <?= number_format($currentCount['web']) ?> · 软件内更新 <?= number_format($currentCount['update']) ?></p>
        </div>
        <p class="count-note">官网下载和软件里的升级都会下载这一份程序，各算一次。只检查更新、没有下载程序的不会计数。这个数字从开始统计之后计算。</p>
        <?php if (($current['notes'] ?? '') !== ''): ?><p class="notes"><?= h((string) $current['notes']) ?></p><?php endif; ?>
        <div class="hero-actions"><a class="button" href="/admin/publish.php">发布新版本</a></div>
    <?php endif; ?>
</section>
<?php if ($catalog['releases']): ?>
<section class="sheet">
    <h2>已上传的版本</h2>
    <table>
        <thead><tr><th>版本</th><th>时间</th><th>大小</th><th>下载次数</th><th></th></tr></thead>
        <tbody>
        <?php foreach ($catalog['releases'] as $release): ?>
            <?php if (!is_array($release)) { continue; } ?>
            <tr>
                <td><?= h((string) ($release['version'] ?? '')) ?><?= ($release['version'] ?? '') === $catalog['current'] ? ' · 当前' : '' ?></td>
                <td><?= h(format_release_time((string) ($release['releaseDate'] ?? ''))) ?></td>
                <td><?= h(format_bytes((int) ($release['size'] ?? 0))) ?></td>
                <?php $rowCount = $downloadStats[(string) ($release['version'] ?? '')] ?? ['web' => 0, 'update' => 0, 'total' => 0]; ?>
                <td><?= number_format($rowCount['total']) ?><span class="count-split">官网 <?= number_format($rowCount['web']) ?> · 更新 <?= number_format($rowCount['update']) ?></span></td>
                <td>
                    <?php if (($release['version'] ?? '') !== $catalog['current']): ?>
                        <form class="table-form" method="post">
                            <?= csrf_field() ?>
                            <input type="hidden" name="version" value="<?= h((string) $release['version']) ?>">
                            <button type="submit">改回这个版本</button>
                        </form>
                    <?php endif; ?>
                </td>
            </tr>
        <?php endforeach; ?>
        </tbody>
    </table>
</section>
<?php endif; ?>
<?php
render_admin_end();
