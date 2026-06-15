<?php
/**
 * qa_switch.php — Agora QA fast-path hook for a per-dev sddev box.
 *
 * Switches the live Yii checkout to a task branch (so the QA agent can test it)
 * or restores the base branch — in seconds, no Docker rebuild.
 *
 * SECURITY: token-gated (constant-time compare), strict branch/remote
 * validation (no shell injection), confined to one repo dir. Still: this runs
 * git + build commands on your box. Keep the token secret, serve over HTTPS,
 * and only expose on your own dev box.
 *
 * DEPLOY (on <name>.sddev.uz):
 *   1. Put this file in the web root (or behind a tiny route).
 *   2. Set the token + repo dir below (or via env QA_SWITCH_TOKEN / QA_REPO_DIR).
 *   3. In the repo (QA_REPO_DIR), configure two git remotes:
 *        origin -> upstream (e.g. github.com/azizkh/sd)
 *        fork   -> the dev's fork (e.g. github.com/jamshidtulaganov/sd-main)
 *      and make sure the web user can run git/composer/php and write runtime/.
 *   4. Tune $REBUILD_STEPS for your stack (Yii 1.x defaults below).
 *
 * CALL:
 *   POST /qa_switch.php?branch=btx-123&remote=fork    (header: X-QA-Token: <token>)
 *   POST /qa_switch.php?branch=billing&remote=origin  (restore base)
 * RETURNS: {"ok":true,"branch":"...","log":"..."} | {"ok":false,"error":"...","log":"..."}
 */

header('Content-Type: application/json');

// ---- config (edit on the box, or set via env) ----
$TOKEN    = getenv('QA_SWITCH_TOKEN') ?: 'CHANGE_ME';
$REPO_DIR = getenv('QA_REPO_DIR') ?: '/var/www/sd';
$ALLOWED_REMOTES = ['fork', 'origin'];
// Commands run (in order) after the branch is checked out. Tune for your stack.
$REBUILD_STEPS = [
    'composer install --no-dev --no-interaction --prefer-dist --optimize-autoloader',
    'php protected/yiic.php migrate up --interactive=0',   // Yii 1.x migrations
    'rm -rf protected/runtime/cache/* 2>/dev/null; true',  // clear cache (best-effort)
];

function out($arr, $code = 200) { http_response_code($code); echo json_encode($arr); exit; }

// ---- auth: constant-time token check ----
$provided = $_SERVER['HTTP_X_QA_TOKEN'] ?? ($_GET['token'] ?? '');
if ($TOKEN === 'CHANGE_ME' || !is_string($provided) || !hash_equals($TOKEN, $provided)) {
    out(['ok' => false, 'error' => 'unauthorized'], 401);
}

// ---- validate inputs (defend against shell injection) ----
$branch = (string)($_GET['branch'] ?? '');
$remote = (string)($_GET['remote'] ?? 'fork');
if (!preg_match('#^[A-Za-z0-9._/-]{1,100}$#', $branch)) {
    out(['ok' => false, 'error' => 'invalid branch'], 400);
}
if (!in_array($remote, $ALLOWED_REMOTES, true)) {
    out(['ok' => false, 'error' => 'invalid remote'], 400);
}
if (!is_dir($REPO_DIR . '/.git')) {
    out(['ok' => false, 'error' => 'repo not found at ' . $REPO_DIR], 500);
}

// ---- run ----
$log = '';
function run($cmd, $cwd, &$log) {
    $full = 'cd ' . escapeshellarg($cwd) . ' && ' . $cmd . ' 2>&1';
    exec($full, $o, $rc);
    $log .= '$ ' . $cmd . "\n" . implode("\n", $o) . "\n";
    return $rc;
}

$ra = escapeshellarg($remote);
$ba = escapeshellarg($branch);

// fetch + hard-align the local branch to <remote>/<branch>
$switch = [
    "git fetch $ra --prune",
    "git checkout -B $ba $ra/$ba",
    "git reset --hard $ra/$ba",
];
foreach (array_merge($switch, $REBUILD_STEPS) as $s) {
    if (run($s, $REPO_DIR, $log) !== 0) {
        out(['ok' => false, 'branch' => $branch, 'error' => "step failed: $s", 'log' => $log], 500);
    }
}

out(['ok' => true, 'branch' => $branch, 'remote' => $remote, 'log' => $log]);
