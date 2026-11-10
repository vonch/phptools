<?php

declare(strict_types=1);

require dirname(__DIR__, 2) . '/lib/bootstrap.php';
require dirname(__DIR__, 2) . '/lib/release.php';
require dirname(__DIR__, 2) . '/lib/auth.php';
require dirname(__DIR__, 2) . '/lib/layout.php';

site_session();
if (site_config() !== null) {
    redirect('/admin/login.php');
}

$error = '';
$username = '';
$publicBase = 'https://phptools.wuyouzhilian.cn';
if ($_SERVER['REQUEST_METHOD'] === 'POST') {
    csrf_check();
    $username = trim((string) ($_POST['username'] ?? ''));
    $password = (string) ($_POST['password'] ?? '');
    $confirm = (string) ($_POST['confirm'] ?? '');
    $publicBase = rtrim(trim((string) ($_POST['public_base'] ?? '')), '/');
    if ($username === '' || strlen($username) > 64) {
        $error = '请填写 64 个字符以内的管理员账号。';
    } elseif (strlen($password) < 8) {
        $error = '密码至少 8 个字符。';
    } elseif ($password !== $confirm) {
        $error = '两次输入的密码不一致。';
    } elseif (!valid_public_base($publicBase)) {
        $error = '对外地址必须是不含账号、参数和片段的 HTTPS 地址。';
    } else {
        try {
            save_site_config($username, $password, $publicBase);
            flash('管理员已创建，请登录。');
            redirect('/admin/login.php');
        } catch (RuntimeException $exception) {
            $error = $exception->getMessage();
        }
    }
}

render_admin_start('创建管理员');
?>
<section class="sheet">
    <p class="eyebrow">第一次打开</p>
    <h1>创建发布账号。</h1>
    <p>这个后台只用来发布 PHPTools 的新版本。账号保存在服务器的站点配置里，不会出现在官网页面上。</p>
    <?php if ($error !== ''): ?><p class="notice notice-error"><?= h($error) ?></p><?php endif; ?>
    <form method="post">
        <?= csrf_field() ?>
        <label>管理员账号<input name="username" type="text" autocomplete="username" value="<?= h($username) ?>" required></label>
        <label>密码<input name="password" type="password" autocomplete="new-password" required></label>
        <label>再输入一次密码<input name="confirm" type="password" autocomplete="new-password" required></label>
        <label>网站对外地址<input name="public_base" type="url" value="<?= h($publicBase) ?>" required></label>
        <p>软件会把更新清单固定读成这个地址下的 <span class="mono">/update.json</span>，程序文件固定为 <span class="mono">/PHPTools.exe</span>。</p>
        <button class="button" type="submit">创建并继续</button>
    </form>
</section>
<?php
render_admin_end();
