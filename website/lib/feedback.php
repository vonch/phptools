<?php

declare(strict_types=1);

const FEEDBACK_IMAGE_LIMIT = 3;
const FEEDBACK_IMAGE_INPUT_BYTES = 8388608;
const FEEDBACK_IMAGE_OUTPUT_BYTES = 1048576;
const FEEDBACK_IMAGE_MAX_PIXELS = 8000000;

function feedback_root(): string
{
    return SITE_STORAGE . '/feedback';
}

function feedback_save(array $fields, array $images, string $source, string $ip): string
{
    $report = feedback_fields($fields);
    $files = feedback_images($images);
    feedback_rate_limit($ip);
    $id = gmdate('Ymd') . '-' . bin2hex(random_bytes(6));
    $dir = feedback_root() . '/' . $id;
    if (!is_dir(feedback_root()) && !mkdir(feedback_root(), 0700, true)) {
        throw new RuntimeException('暂时无法保存反馈。');
    }
    if (!mkdir($dir, 0700)) {
        throw new RuntimeException('暂时无法保存反馈。');
    }
    $saved = [];
    foreach ($files as $index => $file) {
        $name = ($index + 1) . '.' . $file['ext'];
        if (file_put_contents($dir . '/' . $name, $file['bytes']) === false) {
            feedback_remove($dir);
            throw new RuntimeException('图片没有保存成功，请重试。');
        }
        $saved[] = $name;
    }
    $report['id'] = $id;
    $report['source'] = $source;
    $report['images'] = $saved;
    $report['createdAt'] = gmdate('Y-m-d\TH:i:s\Z');
    $json = json_encode($report, JSON_UNESCAPED_UNICODE | JSON_UNESCAPED_SLASHES | JSON_PRETTY_PRINT);
    if ($json === false || file_put_contents($dir . '/report.json', $json . "\n") === false) {
        feedback_remove($dir);
        throw new RuntimeException('反馈没有保存成功，请重试。');
    }
    return $id;
}

function feedback_fields(array $fields): array
{
    $kind = (string) ($fields['kind'] ?? '');
    if ($kind !== 'bug' && $kind !== 'suggestion') {
        throw new RuntimeException('请选择建议或 BUG。');
    }
    $text = [];
    foreach (['title', 'version', 'steps', 'expected', 'actual', 'problem', 'proposal', 'contact'] as $key) {
        $text[$key] = feedback_text((string) ($fields[$key] ?? ''), $key === 'title' ? 60 : ($key === 'contact' || $key === 'version' ? 80 : 2000));
    }
    if (mb_strlen($text['title']) < 2) {
        throw new RuntimeException('请用一句话写清标题。');
    }
    if ($kind === 'bug') {
        foreach (['steps' => '操作步骤', 'expected' => '期望结果', 'actual' => '实际结果'] as $key => $label) {
            if (mb_strlen($text[$key]) < 4) {
                throw new RuntimeException('BUG 需要写清' . $label . '。');
            }
        }
    } else {
        if (mb_strlen($text['problem']) < 4 || mb_strlen($text['proposal']) < 4) {
            throw new RuntimeException('建议需要写清现在的问题和希望的做法。');
        }
    }
    $text['kind'] = $kind;
    return $text;
}

function feedback_text(string $value, int $limit): string
{
    $value = str_replace(["\r\n", "\r"], "\n", $value);
    $value = preg_replace('/[^\P{C}\n\t]/u', '', $value) ?? '';
    $value = trim($value);
    if (mb_strlen($value) > $limit) {
        throw new RuntimeException('有一段内容超过 ' . $limit . ' 个字。');
    }
    return $value;
}

function feedback_images(array $images): array
{
    if (count($images) > FEEDBACK_IMAGE_LIMIT) {
        throw new RuntimeException('最多上传 ' . FEEDBACK_IMAGE_LIMIT . ' 张图片。');
    }
    $saved = [];
    foreach ($images as $image) {
        $bytes = (string) ($image['bytes'] ?? '');
        if ($bytes === '') {
            continue;
        }
        $saved[] = feedback_process_image($bytes);
    }
    return $saved;
}

function feedback_process_image(string $bytes): array
{
    if (strlen($bytes) > FEEDBACK_IMAGE_INPUT_BYTES) {
        throw new RuntimeException('单张原图不能超过 8 MB。');
    }
    if (!function_exists('imagecreatefromstring') || !function_exists('imagejpeg')) {
        throw new RuntimeException('暂时无法处理图片。');
    }
    $info = @getimagesizefromstring($bytes);
    $type = (int) ($info[2] ?? 0);
    if (!in_array($type, [IMAGETYPE_JPEG, IMAGETYPE_PNG, IMAGETYPE_GIF, IMAGETYPE_WEBP], true)) {
        throw new RuntimeException('图片只接受 JPG、PNG、GIF 或 WebP。');
    }
    $width = (int) ($info[0] ?? 0);
    $height = (int) ($info[1] ?? 0);
    if ($width < 1 || $height < 1 || $width > 8000 || $height > 8000 || ($width * $height) > FEEDBACK_IMAGE_MAX_PIXELS) {
        throw new RuntimeException('图片尺寸过大。');
    }
    $source = @imagecreatefromstring($bytes);
    if ($source === false) {
        throw new RuntimeException('有一张图片无法读取。');
    }
    $working = null;
    try {
        $working = feedback_flatten_image($source, 1920);
        $quality = 85;
        for ($attempt = 0; $attempt < 12; $attempt++) {
            $encoded = feedback_jpeg_bytes($working, $quality);
            if ($encoded !== null && strlen($encoded) <= FEEDBACK_IMAGE_OUTPUT_BYTES && @getimagesizefromstring($encoded) !== false) {
                return ['bytes' => $encoded, 'ext' => 'jpg'];
            }
            if ($quality > 55) {
                $quality -= 10;
                continue;
            }
            $edge = max(imagesx($working), imagesy($working));
            $nextEdge = (int) floor($edge * 0.75);
            if ($nextEdge < 480) {
                break;
            }
            $scaled = feedback_flatten_image($working, $nextEdge);
            imagedestroy($working);
            $working = $scaled;
            $quality = 75;
        }
        throw new RuntimeException('这张图片无法压到 1 MB 以内。');
    } finally {
        if ($working instanceof GdImage || is_resource($working)) {
            imagedestroy($working);
        }
        imagedestroy($source);
    }
}

function feedback_flatten_image($source, int $maxEdge)
{
    $width = imagesx($source);
    $height = imagesy($source);
    $edge = max($width, $height);
    $targetWidth = $width;
    $targetHeight = $height;
    if ($edge > $maxEdge) {
        $scale = $maxEdge / $edge;
        $targetWidth = max(1, (int) round($width * $scale));
        $targetHeight = max(1, (int) round($height * $scale));
    }
    $canvas = imagecreatetruecolor($targetWidth, $targetHeight);
    if ($canvas === false) {
        throw new RuntimeException('暂时无法处理图片。');
    }
    $white = imagecolorallocate($canvas, 255, 255, 255);
    imagefill($canvas, 0, 0, $white);
    $copied = $targetWidth === $width && $targetHeight === $height
        ? imagecopy($canvas, $source, 0, 0, 0, 0, $width, $height)
        : imagecopyresampled($canvas, $source, 0, 0, 0, 0, $targetWidth, $targetHeight, $width, $height);
    if (!$copied) {
        imagedestroy($canvas);
        throw new RuntimeException('暂时无法处理图片。');
    }
    return $canvas;
}

function feedback_jpeg_bytes($image, int $quality): ?string
{
    ob_start();
    $ok = imagejpeg($image, null, $quality);
    $bytes = ob_get_clean();
    if (!$ok || !is_string($bytes) || !str_starts_with($bytes, "\xFF\xD8\xFF")) {
        return null;
    }
    return $bytes;
}

function feedback_rate_limit(string $ip): void
{
    $dir = feedback_root() . '/limits';
    if (!is_dir($dir) && !mkdir($dir, 0700, true)) {
        throw new RuntimeException('暂时无法保存反馈。');
    }
    $path = $dir . '/' . hash('sha256', $ip) . '.json';
    $handle = fopen($path, 'c+');
    if ($handle === false) {
        throw new RuntimeException('暂时无法保存反馈。');
    }
    try {
        if (!flock($handle, LOCK_EX)) {
            throw new RuntimeException('暂时无法保存反馈。');
        }
        $raw = stream_get_contents($handle);
        $decoded = json_decode(is_string($raw) ? $raw : '', true);
        $now = time();
        $hits = [];
        if (is_array($decoded)) {
            foreach ($decoded as $hit) {
                if (is_int($hit) && $hit > $now - 3600) {
                    $hits[] = $hit;
                }
            }
        }
        if (count($hits) >= 8) {
            throw new RuntimeException('提交过于频繁，请一小时后再试。');
        }
        $hits[] = $now;
        $json = json_encode($hits);
        if ($json === false || !rewind($handle) || !ftruncate($handle, 0) || fwrite($handle, $json) === false) {
            throw new RuntimeException('暂时无法保存反馈。');
        }
    } finally {
        flock($handle, LOCK_UN);
        fclose($handle);
    }
}

function feedback_list(): array
{
    $root = feedback_root();
    if (!is_dir($root)) {
        return [];
    }
    $items = [];
    foreach (scandir($root) ?: [] as $name) {
        if (!preg_match('/^[0-9]{8}-[a-f0-9]{12}$/', $name)) {
            continue;
        }
        $report = feedback_read($name);
        if ($report !== null) {
            $items[] = $report;
        }
    }
    usort($items, static fn (array $left, array $right): int => strcmp((string) $right['id'], (string) $left['id']));
    return $items;
}

function feedback_read(string $id): ?array
{
    if (!preg_match('/^[0-9]{8}-[a-f0-9]{12}$/', $id)) {
        return null;
    }
    $path = feedback_root() . '/' . $id . '/report.json';
    if (!is_file($path)) {
        return null;
    }
    $decoded = json_decode((string) file_get_contents($path), true);
    return is_array($decoded) ? $decoded : null;
}

function feedback_image_path(string $id, string $name): ?string
{
    if (feedback_read($id) === null || !preg_match('/^[1-3]\.(jpg|png|gif|webp)$/', $name)) {
        return null;
    }
    $path = feedback_root() . '/' . $id . '/' . $name;
    return is_file($path) ? $path : null;
}

function feedback_remove(string $dir): void
{
    foreach (glob($dir . '/*') ?: [] as $file) {
        if (is_file($file)) {
            unlink($file);
        }
    }
    @rmdir($dir);
}
