<?php

declare(strict_types=1);

require dirname(__DIR__, 2) . '/lib/bootstrap.php';
require dirname(__DIR__, 2) . '/lib/auth.php';

site_session();
$_SESSION = [];
session_destroy();
redirect('/admin/login.php');
