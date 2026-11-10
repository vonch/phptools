<?php

declare(strict_types=1);

require dirname(__DIR__, 2) . '/lib/bootstrap.php';
require dirname(__DIR__, 2) . '/lib/auth.php';
require dirname(__DIR__, 2) . '/lib/feedback.php';

site_session();
require_setup_or_login();

$path = feedback_image_path(trim((string) ($_GET['id'] ?? '')), trim((string) ($_GET['file'] ?? '')));
if ($path === null) {
    http_response_code(404);
    exit('没有这张图片。');
}
$type = [
    'jpg' => 'image/jpeg',
    'png' => 'image/png',
    'gif' => 'image/gif',
    'webp' => 'image/webp',
][pathinfo($path, PATHINFO_EXTENSION)] ?? 'application/octet-stream';
header('Content-Type: ' . $type);
header('X-Content-Type-Options: nosniff');
header('Content-Length: ' . (string) filesize($path));
readfile($path);
