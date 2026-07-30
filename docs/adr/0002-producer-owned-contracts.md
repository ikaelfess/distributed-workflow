# Keep published contracts with their producers

Each service owns and versions the HTTP, gRPC, and event schemas it publishes rather than placing cross-service contracts in a centralized directory. Producer ownership keeps contract evolution with the context that defines the behavior, while versioning and automated compatibility checks let consumers depend on those contracts without creating a central ownership bottleneck.
