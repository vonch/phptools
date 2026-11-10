<?php

declare(strict_types=1);

require dirname(__DIR__, 2) . '/lib/bootstrap.php';
require dirname(__DIR__, 2) . '/lib/auth.php';
require dirname(__DIR__, 2) . '/lib/release.php';
require dirname(__DIR__, 2) . '/lib/feedback.php';
require dirname(__DIR__, 2) . '/lib/layout.php';

site_session();
require_setup_or_login();

$selected = feedback_read(trim((string) ($_GET['id'] ?? '')));
render_admin_start('反馈');
?>
<section class="sheet">
    <p class="eyebrow">软件和官网提交</p>
    <h1>建议和 BUG</h1>
    <?php $items = feedback_list(); ?>
    <?php if (!$items): ?>
        <p>还没有反馈。</p>
    <?php else: ?>
        <table>
            <thead><tr><th>时间</th><th>类型</th><th>标题</th><th>版本</th><th></th></tr></thead>
            <tbody>
            <?php foreach ($items as $item): ?>
                <tr>
                    <td><?= h(format_release_time((string) ($item['createdAt'] ?? ''))) ?></td>
                    <td><?= ($item['kind'] ?? '') === 'bug' ? 'BUG' : '建议' ?></td>
                    <td><?= h((string) ($item['title'] ?? '')) ?></td>
                    <td><?= h((string) ($item['version'] ?? '')) ?></td>
                    <td><a href="/admin/feedback.php?id=<?= h((string) $item['id']) ?>">查看</a></td>
                </tr>
            <?php endforeach; ?>
            </tbody>
        </table>
    <?php endif; ?>
</section>
<?php if ($selected): ?>
<section class="sheet">
    <h2><?= h((string) ($selected['title'] ?? '')) ?></h2>
    <p><?= ($selected['kind'] ?? '') === 'bug' ? 'BUG' : '建议' ?> · <?= h((string) ($selected['version'] ?? '')) ?> · <?= ($selected['source'] ?? '') === 'app' ? '软件内' : '官网' ?></p>
    <?php
    $blocks = ($selected['kind'] ?? '') === 'bug'
        ? ['steps' => '操作步骤', 'expected' => '期望结果', 'actual' => '实际结果']
        : ['problem' => '现在的问题', 'proposal' => '希望的做法'];
    foreach ($blocks as $key => $label):
        if (($selected[$key] ?? '') === '') {
            continue;
        }
        ?>
        <h3><?= h($label) ?></h3>
        <p class="notes"><?= h((string) $selected[$key]) ?></p>
    <?php endforeach; ?>
    <?php if (($selected['contact'] ?? '') !== ''): ?>
        <h3>联系方式</h3>
        <p><?= h((string) $selected['contact']) ?></p>
    <?php endif; ?>
    <?php if (!empty($selected['images']) && is_array($selected['images'])): ?>
        <div class="feedback-images">
            <?php foreach ($selected['images'] as $image): ?>
                <?php if (!is_string($image)) { continue; } ?>
                <img src="/admin/feedback-image.php?id=<?= h((string) $selected['id']) ?>&amp;file=<?= h($image) ?>" alt="">
            <?php endforeach; ?>
        </div>
    <?php endif; ?>
</section>
<?php endif; ?>
<?php
render_admin_end();
