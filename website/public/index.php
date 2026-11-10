<?php

declare(strict_types=1);

require dirname(__DIR__) . '/lib/bootstrap.php';
require dirname(__DIR__) . '/lib/release.php';
require dirname(__DIR__) . '/lib/layout.php';

$release = current_release();
render_public_start('PHPTools · 本机 PHP 开发环境', 'home');
?>
<section class="hero">
    <div class="hero-copy">
        <p class="eyebrow">Windows 本地开发</p>
        <h1>把 PHP 环境留在自己的电脑上。</h1>
        <p class="lede">PHPTools 管理这台电脑上的 Nginx、PHP、MySQL 和 Redis。站点、数据库和密码记在你选择的环境目录里，交给别人的只有程序本身。</p>
        <div class="hero-actions">
            <?php if ($release): ?>
                <a class="button" href="/PHPTools.exe">下载 Windows 版 <?= h($release['version']) ?></a>
            <?php endif; ?>
            <a class="button button-quiet" href="/download.php">查看版本与校验</a>
        </div>
        <ul class="hero-points">
            <li>服务默认只监听 127.0.0.1</li>
            <li>组件按需下载，不随软件打包</li>
            <li>升级只替换程序，环境目录保留</li>
        </ul>
    </div>
    <figure class="hero-shot">
        <img src="/assets/screens/overview.jpg" alt="PHPTools 环境概览界面" width="1360" height="860">
        <figcaption>实际界面。实例名称已改为示例。</figcaption>
    </figure>
</section>

<section class="facts" aria-label="组件怎么一起工作">
    <article>
        <b>PHP</b>
        <span>多个版本可以同时开着</span>
    </article>
    <article>
        <b>Nginx</b>
        <span>同一时间只运行一个实例</span>
    </article>
    <article>
        <b>MySQL</b>
        <span>各自的端口、数据目录和密码</span>
    </article>
    <article>
        <b>Redis</b>
        <span>按实例单独启动</span>
    </article>
</section>

<section class="section shots">
    <div class="section-head">
        <p class="eyebrow">界面</p>
        <h2>站点、组件都在这一个窗口里。</h2>
        <p>下面是软件里的真实页面。站点名称和带有本机环境来源的实例名已改成示例，其余界面保持原样。</p>
    </div>
    <div class="shot-grid">
        <figure>
            <img src="/assets/screens/sites.jpg" alt="站点管理页面，站点名称已改为示例" width="1360" height="860">
            <figcaption>站点管理</figcaption>
        </figure>
        <figure>
            <img src="/assets/screens/packages.jpg" alt="组件中心中的 PHP 版本列表" width="1360" height="860">
            <figcaption>组件中心</figcaption>
        </figure>
    </div>
</section>

<section class="section" id="capabilities">
    <div class="section-head">
        <p class="eyebrow">功能</p>
        <h2>从装组件到打开站点。</h2>
        <p>软件管的是本机服务。PHP、Nginx、MySQL 这些程序在组件中心另行下载，不放进 PHPTools 安装包。</p>
    </div>
    <div class="feature-grid">
        <article class="card">
            <span>01</span>
            <h3>组件中心</h3>
            <p>按需安装 Nginx、PHP、MySQL、Redis。PHP 核对官方 Windows 包的摘要，Nginx 和 MySQL 核对官方签名。下载中断、校验失败或解压失败会清掉临时文件。</p>
        </article>
        <article class="card">
            <span>02</span>
            <h3>服务实例</h3>
            <p>选好版本后创建实例并启动。PHP 可以多版本同时运行。每个 MySQL 使用自己的端口、数据目录和密码。Nginx 同时只保留一个实例。</p>
        </article>
        <article class="card">
            <span>03</span>
            <h3>站点</h3>
            <p>填写域名、项目目录、入口目录、端口和 PHP 实例。静态站点不必选择 PHP。伪静态可以套用 Laravel、ThinkPHP、WordPress、Yii2、Symfony、CodeIgniter 4、Typecho，填入后仍可修改。</p>
        </article>
        <article class="card">
            <span>04</span>
            <h3>数据库</h3>
            <p>新建时只填库名和密码。记录先写进 PHPTools，再同步到所选 MySQL，并创建同名的 localhost 账号。也可以导入、导出 SQL，以及管理账号权限。其他工具建的库不会出现在这个列表里。</p>
        </article>
        <article class="card">
            <span>05</span>
            <h3>环境检测</h3>
            <p>启动或服务操作失败后会做轻量检查。手动检测还会核对配置、监听端口、服务响应和 hosts。能安全修复的项目可以单独修，检测本身不会自动启停服务。</p>
        </article>
        <article class="card">
            <span>06</span>
            <h3>数据留在本机</h3>
            <p>环境目录保存组件、实例、站点、日志和 metadata.db。MySQL 密码放在当前 Windows 用户的凭据管理器里。软件只管理自己创建的服务。</p>
        </article>
    </div>
</section>

<section class="ports">
    <div class="ports-copy">
        <p class="eyebrow">建议端口</p>
        <h2>同类实例分开监听。</h2>
        <p>服务默认只绑定本机。再开一个同类实例时换一个端口。和本机其他 Web 服务同时使用时，站点端口也不能重复，只绑定 127.0.0.1 不能把端口隔开。</p>
    </div>
    <dl class="port-board">
        <div><dt>Nginx</dt><dd>8081</dd></div>
        <div><dt>PHP</dt><dd>9100</dd></div>
        <div><dt>MySQL</dt><dd>3307</dd></div>
        <div><dt>Redis</dt><dd>6380</dd></div>
    </dl>
</section>

<section class="steps">
    <div class="steps-copy">
        <p class="eyebrow">开始使用</p>
        <h2>三步进到项目。</h2>
        <p>运行软件不需要单独安装 Go 或 Node.js。电脑需要 Windows 10/11 64 位，以及 Microsoft Edge WebView2。</p>
    </div>
    <ol class="step-list">
        <li class="step">
            <span>01</span>
            <h3>运行 PHPTools.exe</h3>
            <p>解压发行包后直接打开。缺少 WebView2 时，软件会打开微软的安装页面，装完可以再试一次。</p>
        </li>
        <li class="step">
            <span>02</span>
            <h3>选一个空目录</h3>
            <p>例如 D:\PHPTools。程序会在里面生成环境数据库、组件目录和日志。不要把这个目录放进其他环境的安装目录，也不要选磁盘根目录。</p>
        </li>
        <li class="step">
            <span>03</span>
            <h3>安装组件并建站</h3>
            <p>在组件中心下载需要的版本，创建实例，启动服务，再添加站点。关掉窗口后软件留在托盘，要用“停止服务并退出”才结束。</p>
        </li>
    </ol>
</section>

<?php $changelog = array_slice(changelog_releases(), 0, 3); ?>
<?php if ($changelog): ?>
<section class="section" id="changelog">
    <div class="section-head">
        <p class="eyebrow">更新日志</p>
        <h2>每次发布的说明。</h2>
        <p>日志就是发布版本时填写的更新说明。保存之后会出现在这里，也会写进软件的更新提示。</p>
    </div>
    <div class="log-list">
        <?php foreach ($changelog as $item): ?>
            <article class="log-item">
                <header>
                    <h3><?= h((string) $item['version']) ?><?= !empty($item['mandatory']) ? ' · 重要更新' : '' ?></h3>
                    <time><?= h(format_release_time((string) ($item['releaseDate'] ?? ''))) ?></time>
                </header>
                <p class="notes"><?= h((string) ($item['notes'] ?? '')) ?></p>
            </article>
        <?php endforeach; ?>
    </div>
    <p class="more-log"><a href="/changelog.php">查看全部更新日志</a></p>
</section>
<?php endif; ?>
<section class="section" id="feedback">
    <div class="section-head">
        <p class="eyebrow">反馈</p>
        <h2>按格式提交建议或 BUG。</h2>
        <p>BUG 写清操作步骤、期望结果和实际结果。建议写清现在的问题和希望的做法。最多 3 张截图，提交时会压缩到 1 MB 以内。软件设置里的应用升级也能提交。</p>
    </div>
    <p class="more-log"><a href="/feedback.php">去填写</a></p>
</section>
<section class="band">
    <div>
        <p class="eyebrow">升级</p>
        <h2>新版本只换软件本身。</h2>
        <p>替换 PHPTools 程序，并保留环境目录、站点和数据库。PHP、MySQL 这些组件不会被这次升级改掉。升级前先保存正在改的项目。</p>
    </div>
    <a class="button" href="/download.php">当前版本与校验</a>
</section>
<?php
render_public_end();
