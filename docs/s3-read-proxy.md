# Stream S3 objects through a trusted proxy

`STORAGE_S3_READ_ENDPOINT` is an optional S3 host[:port] for object reads,
including the HEAD used when opening an object and ranged GETs used for seeking.
It uses the primary bucket, credentials, region, SSL setting and addressing
style. Leave it empty to read directly from `STORAGE_S3_ENDPOINT`.

This is an authenticated storage transport, independent of
`DELIVERY_CDN_ENABLED` (public API-edge delivery) and presigned delivery. Vidra
still checks viewer permissions and streams the response through its API. Reads
by background jobs also use the proxy. Writes, deletion, listing, existence
checks, presigning and server-side copies keep using the primary endpoint.
The read endpoint does not change store identity or copy eligibility.

## Backblaze B2 through a Cloudflare Worker

1. Keep the B2 bucket private. Keep `STORAGE_S3_ENDPOINT` on its direct B2 S3
   endpoint and `STORAGE_S3_REGION` on the bucket's actual region.
2. Configure a trusted Worker with the exact bucket and region. A Worker that
   re-signs requests needs the matching S3 credentials in encrypted secret
   bindings. Scope credentials to the intended bucket and rotate them together
   on the server and Worker before they expire. Never put secrets in source.
3. The Worker must validate the incoming SigV4 signature, expiry, host, bucket,
   method and signed headers before fetching B2. Re-sign for B2's upstream host;
   changing only the hostname invalidates the signature. Preserve HEAD, Range,
   conditional headers, content length/type, status codes and streaming bodies.
   Reject invalid/unsigned requests. Do not expose an unauthenticated bucket
   gateway or follow upstream redirects carrying credentials.
4. Bypass edge caching for this authenticated proxy. In particular, Cloudflare
   must not convert an origin HEAD to GET as part of a cache fill: that changes
   the signed method. Review the Workers plan and current provider limits for
   the intended streaming volume. This option itself supplies no edge cache.
5. Verify signed HEAD and ranged GET against the Worker before enabling it.
   Confirm unsigned requests fail and private media still requires Vidra
   authorization. Then set, for example:

   ```dotenv
   STORAGE_S3_READ_ENDPOINT=b2-proxy.example.com
   STORAGE_S3_USE_SSL=true
   STORAGE_S3_FORCE_PATH_STYLE=true
   ```

6. Redeploy the API/worker with the new environment. Check playback, seeking,
   thumbnails, captions, and an unauthorized private-video request. Check a
   server-side copy separately: it must remain on the direct provider endpoint.
   To roll back, clear only `STORAGE_S3_READ_ENDPOINT` and redeploy; objects and
   migration ledgers do not need to move or reset.

The endpoint is trusted operator configuration. It receives signed storage
requests; do not point it at a service you do not control.
