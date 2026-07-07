-- Roll back 0072: drop the durable per-user IPFS mirror re-evaluation queue.
DROP TABLE IF EXISTS media_ipfs_user_reevals;
