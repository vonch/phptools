<?php

declare(strict_types=1);

require dirname(__DIR__) . '/lib/bootstrap.php';
require dirname(__DIR__) . '/lib/release.php';
require dirname(__DIR__) . '/lib/feedback.php';
require dirname(__DIR__) . '/lib/layout.php';

site_session();
$kind = $_POST['kind'] ?? 'bug';
if ($kind !== 'suggestion') {
    $kind = 'bug';
}
$values = [];
foreach (['title', 'version', 'steps', 'expected', 'actual', 'problem', 'proposal', 'contact'] as $key) {
    $values[$key] = is_string($_POST[$key] ?? null) ? $_POST[$key] : '';
}
if ($values['version'] === '') {
    $current = current_release();
    $values['version'] = (string) ($current['version'] ?? '');
}

if ($_SERVER['REQUEST_METHOD'] === 'POST') {
    csrf_check();
    try {
        $images = [];
        $uploads = $_FILES['images'] ?? null;
        if (is_array($uploads) && is_array($uploads['tmp_name'] ?? null)) {
            foreach ($uploads['tmp_name'] as $index => $tmp) {
                $error = (int) ($uploads['error'][$index] ?? UPLOAD_ERR_NO_FILE);
                if ($error === UPLOAD_ERR_NO_FILE || !is_string($tmp) || $tmp === '') {
                    continue;
                }
                if ($error === UPLOAD_ERR_INI_SIZE || $error === UPLOAD_ERR_FORM_SIZE) {
                    throw new RuntimeException('单张原图不能超过 8 MB。');
                }
                if ($error !== UPLOAD_ERR_OK || !is_uploaded_file($tmp)) {
                    throw new RuntimeException('有一张图片没有上传完整。');
                }
                $bytes = file_get_contents($tmp);
                if (!is_string($bytes)) {
                    throw new RuntimeException('有一张图片无法读取。');
                }
                $images[] = ['bytes' => $bytes];
            }
        }
        $id = feedback_save(array_merge($values, ['kind' => $kind]), $images, 'web', (string) ($_SERVER['REMOTE_ADDR'] ?? ''));
        flash('已收到，编号 ' . $id . '。');
        redirect('/feedback.php');
    } catch (RuntimeException $exception) {
        flash($exception->getMessage(), 'error');
    }
}

render_public_start('提交建议或 BUG', 'feedback');
$flash = take_flash();
?>
<section class="download-hero">
    <p class="eyebrow">反馈</p>
    <h1>按这个格式写。</h1>
    <p class="lede">BUG 写清操作步骤、期望结果和实际结果。建议写清现在的问题和希望的做法。图片最多 3 张，提交时会压缩到 1 MB 以内并重新生成，只在后台查看。</p>
</section>
<section class="feedback-wrap">
<section class="sheet">
    <?php if ($flash): ?>
        <p class="notice notice-<?= h((string) ($flash['type'] ?? 'info')) ?>"><?= h((string) ($flash['message'] ?? '')) ?></p>
    <?php endif; ?>
    <form class="feedback-form" method="post" enctype="multipart/form-data">
        <?= csrf_field() ?>
        <div class="kind-row">
            <label class="check"><input type="radio" name="kind" value="bug" <?= $kind === 'bug' ? 'checked' : '' ?>> BUG</label>
            <label class="check"><input type="radio" name="kind" value="suggestion" <?= $kind === 'suggestion' ? 'checked' : '' ?>> 建议</label>
        </div>
        <label>标题<input name="title" required maxlength="60" value="<?= h($values['title']) ?>" placeholder="一句话说明问题"></label>
        <label>软件版本<input name="version" maxlength="80" value="<?= h($values['version']) ?>" placeholder="例如 0.1.32"></label>
        <div class="bug-fields" <?= $kind === 'bug' ? '' : 'hidden' ?>>
            <label>操作步骤<textarea class="short" name="steps" maxlength="2000" placeholder="1. 打开哪个页面&#10;2. 做了什么&#10;3. 出现了什么"><?= h($values['steps']) ?></textarea></label>
            <label>期望结果<textarea class="short" name="expected" maxlength="2000" placeholder="你认为这时应该发生什么"><?= h($values['expected']) ?></textarea></label>
            <label>实际结果<textarea class="short" name="actual" maxlength="2000" placeholder="实际看到的提示、界面或结果"><?= h($values['actual']) ?></textarea></label>
        </div>
        <div class="suggestion-fields" <?= $kind === 'suggestion' ? '' : 'hidden' ?>>
            <label>现在的问题<textarea class="short" name="problem" maxlength="2000" placeholder="现在哪里不方便"><?= h($values['problem']) ?></textarea></label>
            <label>希望的做法<textarea class="short" name="proposal" maxlength="2000" placeholder="希望软件改成什么样"><?= h($values['proposal']) ?></textarea></label>
        </div>
        <label>图片<input type="file" name="images[]" accept="image/jpeg,image/png,image/gif,image/webp" multiple></label>
        <label>联系方式（可选）<input name="contact" maxlength="80" value="<?= h($values['contact']) ?>" placeholder="邮箱或其他方式，可以不填"></label>
        <button class="button" type="submit">提交</button>
    </form>
</section>
</section>
<script>
document.querySelectorAll('input[name="kind"]').forEach((input) => {
    input.addEventListener('change', () => {
        const bug = document.querySelector('input[name="kind"][value="bug"]').checked;
        document.querySelector('.bug-fields').hidden = !bug;
        document.querySelector('.suggestion-fields').hidden = bug;
    });
});
</script>
<?php
render_public_end();
