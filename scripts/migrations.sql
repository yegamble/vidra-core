-- Initial schema for GoTube
-- Users table stores registered accounts
CREATE TABLE IF NOT EXISTS users (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  email VARCHAR(255) NOT NULL UNIQUE,
  password_hash VARCHAR(255) NOT NULL,
  verified BOOLEAN NOT NULL DEFAULT FALSE,
  iota_wallet VARCHAR(255) DEFAULT NULL,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL
) ENGINE=InnoDB;

-- Videos table stores metadata about uploaded videos
CREATE TABLE IF NOT EXISTS videos (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  user_id BIGINT NOT NULL,
  title VARCHAR(255) NOT NULL,
  description TEXT,
  original_name VARCHAR(255) NOT NULL,
  ipfs_cid VARCHAR(255) NOT NULL,
  status ENUM('pending','processing','ready','failed') NOT NULL,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB;

-- Video renditions table stores paths to encoded versions of videos
CREATE TABLE IF NOT EXISTS video_renditions (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  video_id BIGINT NOT NULL,
  resolution VARCHAR(50) NOT NULL,
  bitrate INT NOT NULL,
  file_path VARCHAR(255) NOT NULL,
  created_at DATETIME NOT NULL,
  FOREIGN KEY (video_id) REFERENCES videos(id) ON DELETE CASCADE
) ENGINE=InnoDB;

-- IPFS content references table stores CIDs associated with video files
CREATE TABLE IF NOT EXISTS ipfs_content (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  video_id BIGINT NOT NULL,
  cid VARCHAR(255) NOT NULL UNIQUE,
  file_type VARCHAR(50),
  resolution INTEGER,
  file_size BIGINT,
  pin_status VARCHAR(20) DEFAULT 'pinned',
  gateway_url VARCHAR(1000),
  created_at DATETIME NOT NULL,
  FOREIGN KEY (video_id) REFERENCES videos(id) ON DELETE CASCADE
) ENGINE=InnoDB;