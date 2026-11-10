<?php

declare(strict_types=1);

function config_path(): string
{
    return SITE_STORAGE . '/config.php';
}

function catalog_path(): string
{
    return SITE_STORAGE . '/catalog.json';
}

function site_config(): ?array
{
    $path = config_path();
    if (!is_file($path)) {
        return null;
    }
    $config = require $path;
    return is_array($config) ? $config : null;
}

function save_site_config(string $username, string $password, string $publicBase): void
{
    if (!is_dir(SITE_STORAGE) && !mkdir(SITE_STORAGE, 0700, true)) {
        throw new RuntimeException('无法创建存储目录。');
    }
    $export = var_export([
        'username' => $username,
        'password_hash' => password_hash($password, PASSWORD_DEFAULT),
        'public_base' => $publicBase,
    ], true);
    $tmp = config_path() . '.tmp';
    if (file_put_contents($tmp, "<?php\n\nreturn {$export};\n") === false) {
        throw new RuntimeException('无法保存站点配置。');
    }
    replace_file($tmp, config_path());
}

function valid_public_base(string $value): bool
{
    $parts = parse_url($value);
    if (!is_array($parts)) {
        return false;
    }
    return ($parts['scheme'] ?? '') === 'https'
        && ($parts['host'] ?? '') !== ''
        && empty($parts['user'])
        && empty($parts['pass'])
        && !isset($parts['fragment'])
        && !isset($parts['query']);
}

function valid_version(string $value): bool
{
    return preg_match('/^(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$/', $value) === 1;
}

function compare_versions(string $left, string $right): int
{
    $a = array_map('intval', explode('.', $left));
    $b = array_map('intval', explode('.', $right));
    for ($i = 0; $i < 3; $i++) {
        if ($a[$i] > $b[$i]) {
            return 1;
        }
        if ($a[$i] < $b[$i]) {
            return -1;
        }
    }
    return 0;
}

function load_catalog(): array
{
    if (!is_file(catalog_path())) {
        return ['current' => '', 'releases' => []];
    }
    $data = json_decode((string) file_get_contents(catalog_path()), true);
    if (!is_array($data)) {
        return ['current' => '', 'releases' => []];
    }
    $data['current'] = is_string($data['current'] ?? null) ? $data['current'] : '';
    $data['releases'] = is_array($data['releases'] ?? null) ? $data['releases'] : [];
    return $data;
}

function save_catalog(array $catalog): void
{
    $json = json_encode($catalog, JSON_UNESCAPED_UNICODE | JSON_UNESCAPED_SLASHES | JSON_PRETTY_PRINT);
    if ($json === false) {
        throw new RuntimeException('无法保存版本记录。');
    }
    $tmp = catalog_path() . '.tmp';
    if (file_put_contents($tmp, $json . "\n") === false) {
        throw new RuntimeException('无法保存版本记录。');
    }
    replace_file($tmp, catalog_path());
}

function current_release(): ?array
{
    $catalog = load_catalog();
    foreach ($catalog['releases'] as $release) {
        if (is_array($release) && ($release['version'] ?? '') === $catalog['current']) {
            return $release;
        }
    }
    return null;
}

function release_by_version(string $version): ?array
{
    foreach (load_catalog()['releases'] as $release) {
        if (is_array($release) && ($release['version'] ?? '') === $version) {
            return $release;
        }
    }
    return null;
}

function download_url(): string
{
    $config = site_config();
    $base = rtrim((string) ($config['public_base'] ?? 'https://phptools.wuyouzhilian.cn'), '/');
    return $base . '/PHPTools.exe';
}

function manifest_url(): string
{
    $config = site_config();
    $base = rtrim((string) ($config['public_base'] ?? 'https://phptools.wuyouzhilian.cn'), '/');
    return $base . '/update.json';
}

function upload_error(int $code): string
{
    switch ($code) {
        case UPLOAD_ERR_INI_SIZE:
        case UPLOAD_ERR_FORM_SIZE:
            return '程序超过当前 PHP 上传限制。请把 upload_max_filesize 和 post_max_size 调到至少 100M。';
        case UPLOAD_ERR_PARTIAL:
            return '程序只上传了一部分，请重试。';
        case UPLOAD_ERR_NO_FILE:
            return '请选择 PHPTools.exe。';
        default:
            return '上传失败，请重试。';
    }
}

function assert_windows_exe(string $path): void
{
    $size = filesize($path);
    if ($size === false || $size < 1024 || $size > SITE_MAX_UPLOAD) {
        throw new RuntimeException('程序大小无效，PHPTools 只接受 512 MB 以内的 Windows x64 EXE。');
    }
    $file = fopen($path, 'rb');
    if ($file === false) {
        throw new RuntimeException('无法读取上传的程序。');
    }
    $dos = fread($file, 64);
    if (!is_string($dos) || strlen($dos) < 64 || substr($dos, 0, 2) !== "MZ") {
        fclose($file);
        throw new RuntimeException('上传的文件不是 Windows EXE。');
    }
    $offset = unpack('V', substr($dos, 60, 4));
    $peOffset = (int) ($offset[1] ?? 0);
    if ($peOffset < 64 || fseek($file, $peOffset) !== 0) {
        fclose($file);
        throw new RuntimeException('无法识别这个 EXE 的程序头。');
    }
    $header = fread($file, 26);
    fclose($file);
    if (!is_string($header) || strlen($header) < 26 || substr($header, 0, 4) !== "PE\0\0") {
        throw new RuntimeException('上传的文件不是有效的 Windows EXE。');
    }
    $coff = unpack('vmachine/vsections/Vtime/Vsymbol/Vsymbols/voptional/vcharacteristics', substr($header, 4, 20));
    $magic = unpack('v', substr($header, 24, 2));
    $machine = (int) ($coff['machine'] ?? 0);
    $characteristics = (int) ($coff['characteristics'] ?? 0);
    $executable = 0x0002;
    $dll = 0x2000;
    if ($machine !== 0x8664 || (int) ($magic[1] ?? 0) !== 0x20b || ($characteristics & $executable) === 0 || ($characteristics & $dll) !== 0) {
        throw new RuntimeException('请上传 Windows x64 的 PHPTools.exe，不能使用 ZIP、安装包或 32 位程序。');
    }
}

function publish_uploaded_release(string $version, string $notes, bool $mandatory, array $file): string
{
    if (!valid_version($version)) {
        throw new RuntimeException('版本号必须是三段数字，例如 0.1.21。');
    }
    if (trim($notes) === '') {
        throw new RuntimeException('请填写更新说明。这段文字会显示在官网更新日志里。');
    }
    if (strlen($notes) > 4000) {
        throw new RuntimeException('更新说明不能超过 4000 个字符。');
    }
    $error = (int) ($file['error'] ?? UPLOAD_ERR_NO_FILE);
    if ($error !== UPLOAD_ERR_OK) {
        throw new RuntimeException(upload_error($error));
    }
    $tmp = (string) ($file['tmp_name'] ?? '');
    if ($tmp === '' || !is_uploaded_file($tmp)) {
        throw new RuntimeException('没有收到上传的程序。');
    }
    assert_windows_exe($tmp);
    $stored = 'PHPTools-' . $version . '.exe';
    $destination = SITE_STORAGE . '/releases/' . $stored;
    if (!is_dir(dirname($destination)) && !mkdir(dirname($destination), 0700, true)) {
        throw new RuntimeException('无法创建版本目录。');
    }
    if (!move_uploaded_file($tmp, $destination)) {
        throw new RuntimeException('无法保存上传的程序。');
    }
    try {
        $release = release_from_file($version, $notes, $mandatory, $stored);
        write_public_release($release);
        remember_release($release, true);
    } catch (Throwable $error) {
        throw new RuntimeException($error->getMessage(), 0, $error);
    }
    return version_notice($version);
}

function activate_release(string $version): string
{
    $release = release_by_version($version);
    if ($release === null) {
        throw new RuntimeException('没有这个版本。');
    }
    $path = SITE_STORAGE . '/releases/' . $release['file'];
    if (!is_file($path)) {
        throw new RuntimeException('这个版本的程序文件已经不在服务器上。');
    }
    write_public_release($release);
    remember_release($release, false);
    return version_notice($version);
}

function release_from_file(string $version, string $notes, bool $mandatory, string $stored): array
{
    $path = SITE_STORAGE . '/releases/' . $stored;
    $size = filesize($path);
    $hash = hash_file('sha256', $path);
    if ($size === false || $hash === false) {
        throw new RuntimeException('无法计算程序的大小或 SHA-256。');
    }
    return [
        'version' => $version,
        'notes' => $notes,
        'mandatory' => $mandatory,
        'sha256' => $hash,
        'size' => $size,
        'releaseDate' => gmdate('Y-m-d\TH:i:s\Z'),
        'file' => $stored,
    ];
}

function remember_release(array $release, bool $replace): void
{
    $catalog = load_catalog();
    $releases = [];
    foreach ($catalog['releases'] as $item) {
        if (!is_array($item) || ($item['version'] ?? '') === $release['version']) {
            continue;
        }
        $releases[] = $item;
    }
    if (!$replace) {
        $existing = release_by_version($release['version']);
        if ($existing !== null) {
            $release = $existing;
        }
    }
    array_unshift($releases, $release);
    $catalog['releases'] = $releases;
    $catalog['current'] = $release['version'];
    save_catalog($catalog);
}

function write_public_release(array $release): void
{
    $source = SITE_STORAGE . '/releases/' . $release['file'];
    $exe = SITE_PUBLIC . '/PHPTools.exe';
    $partial = $exe . '.partial';
    if (!copy($source, $partial)) {
        throw new RuntimeException('无法发布 PHPTools.exe。');
    }
    replace_file($partial, $exe);
    $manifest = [
        'version' => $release['version'],
        'downloadUrl' => download_url(),
        'sha256' => $release['sha256'],
        'size' => $release['size'],
        'releaseDate' => $release['releaseDate'],
        'notes' => $release['notes'],
        'mandatory' => (bool) $release['mandatory'],
    ];
    $json = json_encode($manifest, JSON_UNESCAPED_UNICODE | JSON_UNESCAPED_SLASHES | JSON_PRETTY_PRINT);
    if ($json === false) {
        throw new RuntimeException('无法生成更新清单。');
    }
    $tmp = SITE_PUBLIC . '/update.json.tmp';
    if (file_put_contents($tmp, $json . "\n") === false) {
        throw new RuntimeException('无法写入更新清单。');
    }
    replace_file($tmp, SITE_PUBLIC . '/update.json');
}

function version_notice(string $version): string
{
    $catalog = load_catalog();
    $higher = false;
    foreach ($catalog['releases'] as $item) {
        if (!is_array($item) || ($item['version'] ?? '') === $version || !valid_version((string) ($item['version'] ?? ''))) {
            continue;
        }
        if (compare_versions((string) $item['version'], $version) > 0) {
            $higher = true;
        }
    }
    if ($higher) {
        return '已发布 ' . $version . '。已安装的软件只在清单版本更高时提示升级，这个版本不会让更高版本的软件提示更新。';
    }
    return '已发布 ' . $version . '。软件会从更新清单检查到这个版本。';
}

function changelog_releases(): array
{
    $items = [];
    foreach (load_catalog()['releases'] as $release) {
        if (!is_array($release) || !valid_version((string) ($release['version'] ?? ''))) {
            continue;
        }
        $items[] = $release;
    }
    usort($items, function (array $left, array $right): int {
        return compare_versions((string) $right['version'], (string) $left['version']);
    });
    return $items;
}

function format_release_time(string $value): string
{
    $time = date_create($value);
    if ($time === false) {
        return $value;
    }
    $time->setTimezone(new DateTimeZone('Asia/Shanghai'));
    return $time->format('Y-m-d H:i');
}

function stats_path(): string
{
    return SITE_STORAGE . '/stats.json';
}

function load_download_stats(): array
{
    if (!is_file(stats_path())) {
        return [];
    }
    $data = json_decode((string) file_get_contents(stats_path()), true);
    $versions = is_array($data['versions'] ?? null) ? $data['versions'] : [];
    $clean = [];
    foreach ($versions as $version => $row) {
        if (!is_string($version) || !valid_version($version) || !is_array($row)) {
            continue;
        }
        $web = max(0, (int) ($row['web'] ?? 0));
        $update = max(0, (int) ($row['update'] ?? 0));
        $clean[$version] = ['web' => $web, 'update' => $update, 'total' => $web + $update];
    }
    return $clean;
}

function download_totals(string $version): array
{
    $stats = load_download_stats();
    return $stats[$version] ?? ['web' => 0, 'update' => 0, 'total' => 0];
}

function download_channel(): string
{
    $agent = (string) ($_SERVER['HTTP_USER_AGENT'] ?? '');
    if (stripos($agent, 'Go-http-client') === 0) {
        return 'update';
    }
    return 'web';
}

function record_download(string $version, string $channel): void
{
    if (!valid_version($version) || ($channel !== 'web' && $channel !== 'update')) {
        return;
    }
    if (!is_dir(SITE_STORAGE) && !mkdir(SITE_STORAGE, 0700, true)) {
        return;
    }
    $file = fopen(stats_path(), 'c+');
    if ($file === false) {
        return;
    }
    try {
        if (!flock($file, LOCK_EX)) {
            return;
        }
        $raw = stream_get_contents($file);
        $data = json_decode(is_string($raw) ? $raw : '', true);
        if (!is_array($data)) {
            $data = ['versions' => []];
        }
        if (!isset($data['versions']) || !is_array($data['versions'])) {
            $data['versions'] = [];
        }
        $row = is_array($data['versions'][$version] ?? null) ? $data['versions'][$version] : [];
        $row['web'] = max(0, (int) ($row['web'] ?? 0));
        $row['update'] = max(0, (int) ($row['update'] ?? 0));
        $row[$channel]++;
        $data['versions'][$version] = $row;
        $json = json_encode($data, JSON_UNESCAPED_UNICODE | JSON_PRETTY_PRINT);
        if ($json === false) {
            return;
        }
        rewind($file);
        ftruncate($file, 0);
        fwrite($file, $json . "\n");
        fflush($file);
    } finally {
        flock($file, LOCK_UN);
        fclose($file);
    }
}

function replace_file(string $source, string $dest): void
{
    if (is_file($dest) && !unlink($dest)) {
        @unlink($source);
        throw new RuntimeException('无法替换 ' . basename($dest) . '，文件可能正在被下载。');
    }
    if (!rename($source, $dest)) {
        @unlink($source);
        throw new RuntimeException('无法完成文件发布。');
    }
}
