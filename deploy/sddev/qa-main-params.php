<?php
/**
 * qa-main-params.php — fake-license params for a QA checkout of SD (Yii 1.x).
 *
 * Keeps QA OFF real billing: `simulate_host` makes the app behave as if every
 * host has a valid license + generous subscription counts, valid until 2030, so
 * a `qa_switch.php` rebuild on the sddev box never contacts the real billing
 * service. Ported from sd-devops-bots/qa-main-php-patch.txt (which patched the
 * bots' docker/qa/main.php).
 *
 * USAGE — merge into the QA checkout's protected/config/main.php, at the very
 * end, just before it returns the config:
 *
 *     $qaOverride = require __DIR__ . '/qa-main-params.php';
 *     return CMap::mergeArray($config, $qaOverride);   // $config = built config
 *
 * Only do this on the QA box (never on a real billing host).
 */
return array(
    'params' => array(
        'simulate_host' => array(
            'enable'       => true,
            'host'         => 'branch1',
            'id'           => 3000,
            'status'       => true,
            'left_days'    => 30,
            'active_to'    => '2030-09-30',
            'free_to'      => '2030-01-01',
            'balans'       => '55400000000',
            'credit_limit' => '10000000',
            'credit_date'  => '2030-10-20',
            'subcription'  => array(
                'admin'      => array(array('start' => '2022-09-09', 'active' => '2030-12-31', 'count' => 1,  'is_bonus' => false)),
                'agent'      => array(array('start' => '2022-09-09', 'active' => '2030-12-31', 'count' => 20, 'is_bonus' => false)),
                'vansel'     => array(array('start' => '2022-09-09', 'active' => '2030-12-31', 'count' => 20, 'is_bonus' => false)),
                'dastavchik' => array(array('start' => '2022-09-09', 'active' => '2030-12-31', 'count' => 10, 'is_bonus' => false)),
                'merchant'   => array(array('start' => '2022-09-09', 'active' => '2030-12-31', 'count' => 20, 'is_bonus' => false)),
                'seller'     => array(array('start' => '2022-09-09', 'active' => '2030-12-31', 'count' => 10, 'is_bonus' => false)),
                'supervisor' => array(array('start' => '2022-09-09', 'active' => '2030-12-31', 'count' => 10, 'is_bonus' => false)),
            ),
        ),
    ),
);
