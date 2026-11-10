<?php

declare(strict_types=1);

require dirname(__DIR__, 2) . '/lib/bootstrap.php';
require dirname(__DIR__, 2) . '/lib/release.php';
require dirname(__DIR__, 2) . '/lib/auth.php';
require dirname(__DIR__, 2) . '/lib/layout.php';

site_session();
if (site_config() === null) {
    redirect('/admin/setup.php');
}
if (admin_logged_in()) {
    redirect('/admin/');
}

$error = '';
if ($_SERVER['REQUEST_METHOD'] === 'POST') {
    csrf_check();
    $lockedUntil = (int) ($_SESSION['login_locked_until'] ?? 0);
    $failures = (int) ($_SESSION['login_failures'] ?? 0);
    if ($lockedUntil > time()) {
        $error = '尝试次数过多，请十分钟后再登录。';
    } elseif (attempt_login(trim((string) ($_POST['username'] ?? '')), (string) ($_POST['password'] ?? ''))) {
        unset($_SESSION['login_failures']);
        redirect('/admin/');
    } else {
        $failures++;
        if ($failures >= 8) {
            $_SESSION['login_locked_until'] = time() + 600;
            $failures = 0;
        }
        $_SESSION['login_failures'] = $failures;
        $error = '账号或密码不正确。';
    }
}

render_admin_start('登录');
?>
<section class="sheet">
    <p class="eyebrow">版本发布</p>
    <h1>登录后台。</h1>
    <?php if ($error !== ''): ?><p class="notice notice-error"><?= h($error) ?></p><?php endif; ?>
    <form method="post">
        <?= csrf_field() ?>
        <label>账号<input name="username" type="text" autocomplete="username" required></label>
        <label>密码<input name="password" type="password" autocomplete="current-password" required></label>
        <button class="button" type="submit">登录</button>
    </form>
</section>
<?php
render_admin_end();
