<?php

declare(strict_types=1);

function admin_logged_in(): bool
{
    return !empty($_SESSION['admin']);
}

function require_setup_or_login(): void
{
    if (site_config() === null) {
        redirect('/admin/setup.php');
    }
    if (!admin_logged_in()) {
        redirect('/admin/login.php');
    }
}

function attempt_login(string $username, string $password): bool
{
    $config = site_config();
    if ($config === null) {
        return false;
    }
    $knownUser = (string) ($config['username'] ?? '');
    $hash = (string) ($config['password_hash'] ?? '');
    if ($knownUser === '' || $hash === '' || $knownUser !== $username) {
        password_verify($password, $hash);
        return false;
    }
    if (!password_verify($password, $hash)) {
        return false;
    }
    session_regenerate_id(true);
    $_SESSION['admin'] = $knownUser;
    return true;
}
