<?php

declare(strict_types=1);

require dirname(__DIR__, 2) . '/lib/bootstrap.php';
require dirname(__DIR__, 2) . '/lib/feedback.php';

header('Content-Type: application/json; charset=utf-8');

function feedback_json(int $status, array $body): void
{
    http_response_code($status);
    echo json_encode($body, JSON_UNESCAPED_UNICODE | JSON_UNESCAPED_SLASHES) . "\n";
    exit;
}

if ($_SERVER['REQUEST_METHOD'] !== 'POST') {
    feedback_json(405, ['ok' => false, 'error' => '请使用 POST 提交。']);
}
$length = (int) ($_SERVER['CONTENT_LENGTH'] ?? 0);
if ($length <= 0 || $length > 8 * 1024 * 1024) {
    feedback_json(413, ['ok' => false, 'error' => '内容过大或为空。']);
}
$raw = file_get_contents('php://input');
$payload = json_decode(is_string($raw) ? $raw : '', true);
if (!is_array($payload)) {
    feedback_json(400, ['ok' => false, 'error' => '提交内容无法识别。']);
}
$images = [];
foreach (($payload['images'] ?? []) as $image) {
    if (!is_array($image)) {
        continue;
    }
    $decoded = base64_decode((string) ($image['data'] ?? ''), true);
    if ($decoded === false) {
        feedback_json(400, ['ok' => false, 'error' => '有一张图片无法读取。']);
    }
    $images[] = ['bytes' => $decoded];
}
try {
    $id = feedback_save($payload, $images, 'app', (string) ($_SERVER['REMOTE_ADDR'] ?? ''));
} catch (RuntimeException $exception) {
    feedback_json(400, ['ok' => false, 'error' => $exception->getMessage()]);
}
feedback_json(200, ['ok' => true, 'id' => $id]);
