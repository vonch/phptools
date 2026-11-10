<?php

declare(strict_types=1);

require dirname(__DIR__, 2) . '/lib/bootstrap.php';
require dirname(__DIR__, 2) . '/lib/release.php';
require dirname(__DIR__, 2) . '/lib/auth.php';
require dirname(__DIR__, 2) . '/lib/layout.php';

site_session();
require_setup_or_login();

$error = '';
$version = '';
$notes = '';
if ($_SERVER['REQUEST_METHOD'] === 'POST') {
    csrf_check();
    $version = trim((string) ($_POST['version'] ?? ''));
    $notes = trim((string) ($_POST['notes'] ?? ''));
    $mandatory = isset($_POST['mandatory']);
    try {
        $message = publish_uploaded_release($version, $notes, $mandatory, $_FILES['package'] ?? []);
        flash($message);
        redirect('/admin/');
    } catch (RuntimeException $exception) {
        $error = $exception->getMessage();
    }
}

render_admin_start('发布版本');
?>
<section class="sheet">
    <p class="eyebrow">上传 EXE</p>
    <h1>发布一个版本。</h1>
    <p>选择打包出来的 <span class="mono">PHPTools.exe</span>。保存后，网站根目录的 <span class="mono">update.json</span> 和 <span class="mono">PHPTools.exe</span> 会换成这个版本。更新说明会同时写进官网更新日志和软件里的更新提示。已安装的软件只在版本号更高时提示升级。</p>
    <?php if ($error !== ''): ?><p class="notice notice-error"><?= h($error) ?></p><?php endif; ?>
    <form method="post" enctype="multipart/form-data">
        <?= csrf_field() ?>
        <label>版本号<input name="version" type="text" inputmode="decimal" placeholder="0.1.21" value="<?= h($version) ?>" required></label>
        <label>更新说明<textarea name="notes" placeholder="这次更新做了什么" required><?= h($notes) ?></textarea></label>
        <label class="check"><input name="mandatory" type="checkbox"> 标记为重要更新</label>
        <label>PHPTools.exe<input name="package" type="file" accept=".exe,application/vnd.microsoft.portable-executable" required></label>
        <button class="button" type="submit">发布</button>
    </form>
</section>
<?php
render_admin_end();
