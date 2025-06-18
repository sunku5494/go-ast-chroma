import json
import chromadb
from chromadb.utils.embedding_functions import SentenceTransformerEmbeddingFunction
import time

def clean_metadata_for_chromadb(metadata):
    """Clean metadata to ensure all values are ChromaDB-compatible types."""
    cleaned = {}
    for key, value in metadata.items():
        if value is None:
            cleaned[key] = None
        elif isinstance(value, (str, int, float, bool)):
            cleaned[key] = value
        elif isinstance(value, list):
            # Convert lists to comma-separated strings or JSON strings if complex
            if all(isinstance(item, (str, int, float, bool)) for item in value):
                cleaned[key] = ", ".join(str(item) for item in value)
            else:
                cleaned[key] = json.dumps(value) # For lists of dicts or complex objects
        elif isinstance(value, dict):
            # Convert dicts to JSON strings
            cleaned[key] = json.dumps(value)
        else:
            # Convert other types to strings
            cleaned[key] = str(value)
    return cleaned

def main():
    # Load chunks from JSON
    try:
        with open('code_chunks_rewritten_all_symbols.json', 'r') as f:
            chunks = json.load(f)
        print(f"Loaded {len(chunks)} chunks from code_chunks.json")
    except FileNotFoundError:
        print("Error: code_chunks.json file not found. Please run the Go chunking program first.")
        return
    except json.JSONDecodeError:
        print("Error: Invalid JSON in code_chunks.json file.")
        return

    # Initialize ChromaDB HTTP client
    try:
        # Assuming ChromaDB is running on default port 8000 (often) or 8080 as you specified
        client = chromadb.HttpClient(host="localhost", port=8080)
        print("Connected to ChromaDB server at localhost:8080")
    except Exception as e:
        print(f"Error connecting to ChromaDB server: {e}")
        print("Please ensure your ChromaDB server is running at http://localhost:8080")
        return

    # Initialize the embedding function using Qodo-Embed-1
    try:
        embedding_function = SentenceTransformerEmbeddingFunction(model_name="all-MiniLM-L6-v2")
        print("Initialized SentenceTransformerEmbeddingFunction with model: Qodo-Embed-1")
    except Exception as e:
        print(f"Error initializing embedding function: {e}")
        print("Please ensure 'sentence-transformers' library is installed and the model name is correct.")
        print("You might need to run: pip install sentence-transformers")
        return

    # Create or get collection with the specified embedding function
    try:
        # Delete existing collection if it exists (for clean runs)
        try:
            client.delete_collection("go_code_chunks")
            print("Deleted existing 'go_code_chunks' collection")
        except Exception:
            pass  # Collection might not exist

        # Pass the embedding_function to the create_collection method
        collection = client.create_collection(name="go_code_chunks", embedding_function=embedding_function, metadata={"hnsw:search_ef": 100})
        print("Created new 'go_code_chunks' collection with Qodo-Embed-1 embedding function")
    except Exception as e:
        print(f"Error creating collection: {e}")
        return

    # Prepare data for ChromaDB
    ids = []
    documents = []
    metadatas = []
    
    for chunk in chunks:
        ids.append(chunk["id"])
        # Correctly access the 'document' field for the code snippet
        documents.append(chunk["document"]) 
        
        # Start with the metadata dictionary from the Go program's output
        raw_metadata = chunk["metadata"]
        
        # Clean metadata to ensure ChromaDB compatibility
        cleaned_metadata = clean_metadata_for_chromadb(raw_metadata)
        
        # Add top-level fields directly from the Go program's metadata for easier querying
        # Note: The Go program stores these *inside* the 'metadata' dict, not at the chunk's top level.
        # We're promoting them here if they exist, or just keeping them inside the 'metadata' JSON string.
        # It's generally better to keep them as direct metadata fields if possible for filtering.
        
        # Example of promoting 'entity_type' to a top-level metadata field if you want to query it directly
        if "entity_type" in raw_metadata:
            cleaned_metadata["entity_type"] = raw_metadata["entity_type"]
        if "package_name" in raw_metadata:
            cleaned_metadata["package_name"] = raw_metadata["package_name"]
        if "file_path" in raw_metadata:
            cleaned_metadata["file_path"] = raw_metadata["file_path"]
        if "start_line" in raw_metadata:
            cleaned_metadata["start_line"] = raw_metadata["start_line"]
        if "end_line" in raw_metadata:
            cleaned_metadata["end_line"] = raw_metadata["end_line"]
        if "entity_name" in raw_metadata: # Add entity_name for direct search
            cleaned_metadata["entity_name"] = raw_metadata["entity_name"]
        
        # Special handling for 'accessed_entities' which is a list of dicts
        # ChromaDB cannot directly store lists of dicts as metadata.
        # The clean_metadata_for_chromadb function handles converting it to a JSON string.
        # If you need to filter on individual accessed entity names, you might need a more complex
        # strategy, like extracting all accessed entity names into a single comma-separated string,
        # or creating separate chunks for each accessed entity.
        
        metadatas.append(cleaned_metadata)

    # Add chunks to collection in batches
    # ChromaDB will automatically generate embeddings using the provided embedding_function
    batch_size = 100
    total_added = 0
    start_time = time.time()
    print("\nStarting batch addition to ChromaDB...")
    
    try:
        for i in range(0, len(chunks), batch_size):
            end_idx = min(i + batch_size, len(chunks))
            batch_ids = ids[i:end_idx]
            batch_documents = documents[i:end_idx]
            batch_metadatas = metadatas[i:end_idx]
            
            collection.add(
                documents=batch_documents,
                metadatas=batch_metadatas,
                ids=batch_ids
            )
            total_added += len(batch_ids)
            print(f"Added batch {i//batch_size + 1}: {total_added}/{len(chunks)} chunks (embeddings generated)")
        
        time_taken = time.time() - start_time
        print(f"Successfully added all {total_added} chunks with embeddings to the 'go_code_chunks' collection")
        print(f"Time taken to feed all chunks: {time_taken:.2f} seconds")
        
    except Exception as e:
        print(f"An error occurred while adding chunks: {e}")
        return

    # Verify by counting items in the collection
    try:
        count = collection.count()
        print(f"Verification: Collection now contains {count} items")
        
        # Show sample query capabilities
        print("\n=== Sample Query Capabilities ===")
        
        # Query by entity type (renamed from chunk_type in Go output)
        results = collection.query(
            query_texts=["find function declarations"],
            n_results=3,
            where={"entity_type": "function"} # Corrected key
        )
        print(f"Found {len(results['ids'][0]) if results and results['ids'] and results['ids'][0] else 0} function declarations")
        if results and results['documents'] and results['documents'][0]:
            print("Sample result document:", results['documents'][0][0])
            print("Sample result metadata:", results['metadatas'][0][0])


        # Query by package
        if len(chunks) > 0 and "package_name" in chunks[0]["metadata"]:
            sample_package = chunks[0]["metadata"]["package_name"]
            if sample_package:
                results = collection.query(
                    query_texts=["code related to " + sample_package],
                    n_results=3,
                    where={"package_name": sample_package}
                )
                print(f"Found {len(results['ids'][0]) if results and results['ids'] and results['ids'][0] else 0} chunks in package '{sample_package}'")
                if results and results['documents'] and results['documents'][0]:
                    print("Sample result document:", results['documents'][0][0])
                    print("Sample result metadata:", results['metadatas'][0][0])
        
        # Example: Query for code that accesses specific methods (requires JSON parsing in query or flattening metadata)
        # This is more complex because 'accessed_entities' is now a JSON string.
        # For direct filtering on nested data, ChromaDB's WHERE clause works best with flattened data.
        # If you need to filter on "mux.NewRouter" directly, you might store accessed methods as a comma-separated string.
        # Let's adjust clean_metadata_for_chromadb to handle this.

        print("\n=== Ready for semantic search! ===")
        print("You can now query your Go codebase using:")
        print("- Semantic search: collection.query(query_texts=['your search'])")
        print("- Filtered search: collection.query(where={'entity_type': 'function'})")
        print("- Combined search: collection.query(query_texts=['search'], where={'package_name': 'main'})")
        
    except Exception as e:
        print(f"Could not retrieve collection count or perform sample queries: {e}")

if __name__ == "__main__":
    main()
