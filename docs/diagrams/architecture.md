# Athena Architecture Diagrams

This document provides visual architecture references for Athena using Mermaid diagrams.

## 1) System Component Diagram

```mermaid
graph LR
    Client[Web / Mobile / API Clients] --> HTTP[HTTP API: internal/httpapi]
    HTTP --> UC[Usecase Services: internal/usecase/*]
    UC --> Repo[Repositories: internal/repository]
    Repo --> Postgres[(PostgreSQL)]

    HTTP --> Redis[(Redis)]
    UC --> Worker[Schedulers & Workers]
    Worker --> FFmpeg[FFmpeg]
    Worker --> Storage[Storage: Local / S3 / IPFS]

    UC --> Fed[Federation Services]
    Fed --> AP[ActivityPub Instances]
    Fed --> ATP[ATProto / Bluesky]

    UC --> P2P[P2P Distribution]
    P2P --> Torrent[WebTorrent / DHT / PEX]

    Client --> HLS[Live + VOD HLS Playback]
    HTTP --> HLS
    Client --> Chat[WebSocket Chat]
    HTTP --> Chat
```

## 2) Video Upload and Encoding Sequence

```mermaid
sequenceDiagram
    participant C as Client
    participant H as HTTP Handler
    participant U as Upload Usecase
    participant R as Upload/Video Repository
    participant S as Storage
    participant Q as Encoding Queue
    participant W as Encoding Worker

    C->>H: POST /videos/upload/init
    H->>U: InitiateUpload(ctx, request)
    U->>R: Create upload session
    R-->>U: upload_id
    U-->>H: upload metadata
    H-->>C: 200 + upload_id

    loop Chunk uploads
        C->>H: POST /videos/upload/{id}/chunk/{index}
        H->>U: UploadChunk(...)
        U->>S: Persist chunk
        U-->>H: chunk accepted
        H-->>C: 200
    end

    C->>H: POST /videos/upload/{id}/complete
    H->>U: CompleteUpload(...)
    U->>S: Merge and validate
    U->>Q: Enqueue encoding job
    Q-->>W: Pending job
    W->>S: Transcode variants + HLS
    W->>R: Update video status/metadata
```

## 3) Core Entity Relationship Diagram (High-level)

```mermaid
erDiagram
    USERS ||--o{ CHANNELS : owns
    CHANNELS ||--o{ VIDEOS : publishes
    USERS ||--o{ COMMENTS : writes
    VIDEOS ||--o{ COMMENTS : has
    USERS ||--o{ PLAYLISTS : owns
    PLAYLISTS ||--o{ PLAYLIST_ITEMS : contains
    VIDEOS ||--o{ PLAYLIST_ITEMS : referenced_by
    VIDEOS ||--o{ CAPTIONS : has
    USERS ||--o{ SUBSCRIPTIONS : subscribes
    CHANNELS ||--o{ SUBSCRIPTIONS : receives
```
