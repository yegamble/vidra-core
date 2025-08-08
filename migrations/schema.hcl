schema "public" {}

table "videos" {
  schema = schema.public
  column "id"         uuid     [not null, default = gen_random_uuid()]
  column "title"      text     [not null]
  column "ipfs_hash"  text
  column "s3_key"     text
  column "created_at" timestamptz [not null, default = now()]
  primary key ("id")
}
