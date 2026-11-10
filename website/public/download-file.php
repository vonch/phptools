<?php

declare(strict_types=1);

require dirname(__DIR__) . '/lib/bootstrap.php';
require dirname(__DIR__) . '/lib/release.php';

$path = parse_url((string) ($_SERVER['REQUEST_URI'] ?? '/'), PHP_URL_PATH);
if ($path !== '/PHPTools.exe') {
    http_response_code(404);
    header('Content-Type: text/plain; charset=utf-8');
    exit('没有这个文件。');
}

$release = current_release();
$file = SITE_PUBLIC . '/PHPTools.exe';
$size = is_file($file) ? filesize($file) : false;
if ($release === null || $size === false || $size < 1) {
    http_response_code(404);
    header('Content-Type: text/plain; charset=utf-8');
    exit('还没有可下载的版本。');
}

$method = (string) ($_SERVER['REQUEST_METHOD'] ?? 'GET');
if ($method !== 'GET' && $method !== 'HEAD') {
    http_response_code(405);
    header('Allow: GET, HEAD');
    exit;
}

$range = (string) ($_SERVER['HTTP_RANGE'] ?? '');
if ($method === 'GET' && ($range === '' || preg_match('/^bytes=0-/i', $range) === 1)) {
    record_download((string) $release['version'], download_channel());
}

ini_set('zlib.output_compression', '0');
set_time_limit(0);
while (ob_get_level() > 0) {
    ob_end_clean();
}

header('Content-Type: application/octet-stream');
header('Content-Disposition: attachment; filename="PHPTools.exe"');
header('Content-Length: ' . $size);
header('Cache-Control: no-cache');
header('X-Content-Type-Options: nosniff');
header('Accept-Ranges: none');
if ($method === 'HEAD') {
    exit;
}

$handle = fopen($file, 'rb');
if ($handle === false) {
    http_response_code(404);
    header('Content-Type: text/plain; charset=utf-8');
    exit('无法读取程序文件。');
}
fpassthru($handle);
fclose($handle);
