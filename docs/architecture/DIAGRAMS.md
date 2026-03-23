# Architecture Diagrams

This document contains canonical Mermaid diagrams for the Vidra Core system architecture.

## 1) System Architecture

```mermaid
flowchart LR
    Clients["HTTP Clients"] --> Router["Chi Router"]
    Router --> Handlers["HTTP Handlers"]
    Handlers --> Usecases["Use Cases"]
    Usecases --> Repositories["Repositories"]
    Repositories --> Postgres["PostgreSQL"]

    Handlers --> Redis["Redis"]
    Workers["Background Workers"] --> Redis
    Workers --> Scheduler["Schedulers"]
    Scheduler --> Usecases

    Usecases --> IPFS["IPFS"]
    Usecases --> ClamAV["ClamAV"]
    Usecases --> FFmpeg["FFmpeg"]
    Usecases --> ATProto["ATProto"]
    Usecases --> ActivityPub["ActivityPub"]
```

## 2) Core Database ER Diagram

```mermaid
erDiagram
    USER ||--o{ CHANNEL : owns
    USER ||--o{ VIDEO : uploads
    CHANNEL ||--o{ VIDEO : publishes
    USER ||--o{ COMMENT : writes
    VIDEO ||--o{ COMMENT : has
    USER ||--o{ PLAYLIST : owns
    PLAYLIST ||--o{ PLAYLIST_ITEM : contains
    VIDEO ||--o{ PLAYLIST_ITEM : appears_in
    USER ||--o{ SUBSCRIPTION : follows
    CHANNEL ||--o{ SUBSCRIPTION : followed_by

    USER {
        uuid id PK
        string username
        string email
    }

    CHANNEL {
        uuid id PK
        uuid user_id FK
        string name
    }

    VIDEO {
        uuid id PK
        uuid user_id FK
        uuid channel_id FK
        string title
        string status
    }

    COMMENT {
        uuid id PK
        uuid video_id FK
        uuid user_id FK
        uuid parent_id FK
    }

    PLAYLIST {
        uuid id PK
        uuid user_id FK
        string name
    }

    PLAYLIST_ITEM {
        uuid playlist_id FK
        uuid video_id FK
        int position
    }

    SUBSCRIPTION {
        uuid id PK
        uuid user_id FK
        uuid channel_id FK
    }
```

## 3) Video Upload and Encoding Flow

```mermaid
sequenceDiagram
    participant Client
    participant API as API Handler
    participant Upload as Upload Usecase
    participant Redis as Redis
    participant Store as Local Storage
    participant Worker as Encoding Worker
    participant DB as PostgreSQL
    participant IPFS as IPFS

    Client->>API: Initiate upload
    API->>Upload: Create session
    Upload->>DB: Persist upload metadata
    Upload-->>Client: Upload ID + chunk size

    loop Per chunk
        Client->>API: Upload chunk
        API->>Store: Write chunk
        API->>Redis: Track chunk receipt
    end

    Client->>API: Complete upload
    API->>Upload: Validate and merge
    Upload->>Worker: Enqueue encoding job
    Worker->>Worker: Transcode with FFmpeg
    Worker->>IPFS: Pin HLS assets
    Worker->>DB: Mark video completed
```

## 4) Federation Architecture

```mermaid
flowchart LR
    Vidra Core["Vidra Core Instance"] --> Outbox["ActivityPub Outbox"]
    Outbox --> SharedInbox["Remote Shared Inbox"]
    SharedInbox --> RemoteInstances["Remote ActivityPub Instances"]

    RemoteInstances --> Inbox["Vidra Core Inbox"]
    Inbox --> Verify["HTTP Signature Verification"]
    Verify --> Dedup["Deduplicate Activity"]
    Dedup --> Persist["Persist Activity"]

    Vidra Core --> ATWriter["ATProto Publisher"]
    ATWriter --> PDS["BlueSky PDS"]
    Firehose["BlueSky Firehose"] --> ATReader["ATProto Ingestion Worker"]
    ATReader --> Persist
```
