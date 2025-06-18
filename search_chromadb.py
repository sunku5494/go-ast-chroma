import chromadb
from chromadb.utils.embedding_functions import SentenceTransformerEmbeddingFunction
import time
import argparse # Import argparse for command-line arguments

# --- Configuration ---
CHROMA_HOST = "localhost"
CHROMA_PORT = 8080
COLLECTION_NAME = "go_code_chunks"
EMBEDDING_MODEL_NAME = "all-MiniLM-L6-v2"

def run_chroma_search_query(query_type: str = "all"):
    """
    Connects to ChromaDB, ensures a collection exists with sample data,
    and performs a search query based on the specified type.

    Args:
        query_type (str): The type of query to perform.
                          Can be "semantic", "metadata", "document", or "all".
    """
    print(f"Attempting to connect to ChromaDB at http://{CHROMA_HOST}:{CHROMA_PORT}...")
    try:
        client = chromadb.HttpClient(host=CHROMA_HOST, port=CHROMA_PORT)
        # Ping the client to ensure connection
        client.heartbeat()
        print("Successfully connected to ChromaDB.")
    except Exception as e:
        print(f"Error connecting to ChromaDB: {e}")
        print("Please ensure your ChromaDB server is running on the specified host and port.")
        print("You can start a ChromaDB server using: `chroma run --path /path/to/your/db`")
        return

    # --- Initialize Embedding Function ---
    print(f"Initializing embedding function with model: '{EMBEDDING_MODEL_NAME}'...")
    try:
        embedding_function = SentenceTransformerEmbeddingFunction(model_name=EMBEDDING_MODEL_NAME)
        print("Embedding function initialized successfully.")
    except Exception as e:
        print(f"Error initializing embedding function: {e}")
        print(f"Please ensure '{EMBEDDING_MODEL_NAME}' is a valid SentenceTransformer model name,")
        print("and that you have `pip install sentence-transformers`.")
        return

    # --- Get or Create Collection ---
    print(f"Getting or creating collection: '{COLLECTION_NAME}'...")
    try:
        # Note: If you want to ensure hnsw:search_ef is set, do it here.
        # collection = client.get_or_create_collection(
        #     name=COLLECTION_NAME,
        #     metadata={"hnsw:search_ef": 100} # Example: set HNSW search ef
        # )
        collection = client.get_or_create_collection(name=COLLECTION_NAME, metadata={"hnsw:search_ef": 150})
        print(f"Collection '{COLLECTION_NAME}' ready. Document count: {collection.count()}")
    except Exception as e:
        print(f"Error getting/creating collection: {e}")
        return

    # --- Define Search Queries and their types ---
    # We'll use a single query text for demonstration, but you can expand this.
    # For metadata filtering, ensure your added documents have the 'import_pkg' and 'symbol_name' fields.
    base_query_text = "find code chunks related to kubernetes proxy config EndpointsHandler"
    
    # Define target values for metadata filtering (ensure your data has these)
    target_import_pkg = "k8s.io/kubernetes/pkg/proxy/config"
    target_symbol_name = "EndpointsHandler" # For illustrative purposes, though it's in the full path


    # --- Perform Search Queries based on type ---
    print(f"\n--- Performing Search Queries (Type: {query_type.upper()}) ---")

    # Generate embeddings once for efficiency
    query_embeddings = embedding_function([base_query_text])[0]

    # 1. Semantic Search Only
    if query_type in ["semantic", "all"]:
        print(f"\n--- Running Semantic Search (Query: '{base_query_text}') ---")
        try:
            results = collection.query(
                query_embeddings=[query_embeddings],
                n_results=5, # Fetch top 5 most relevant results semantically
                include=['documents', 'metadatas', 'distances']
            )

            if not results['documents'] or not results['documents'][0]: # Check for empty results
                print("  No relevant snippets found with semantic search.")
            else:
                for i, doc in enumerate(results['documents'][0]):
                    distance = results['distances'][0][i]
                    metadata = results['metadatas'][0][i]
                    print(f"  Result {i+1} (Distance: {distance:.4f}):")
                    print(f"    File: {metadata.get('file_path', 'N/A')}")
                    print(f"    Import Pkg: {metadata.get('import_pkg', 'N/A')}")
                    print(f"    Symbol Name: {metadata.get('symbol_name', 'N/A')}")
                    print("    --- Code Snippet ---")
                    print(doc)
                    print("    --------------------")
        except Exception as e:
            print(f"  Error during semantic query: {e}")

    # 2. Filtering by Metadata (Semantic + Metadata Filter)
    if query_type in ["metadata", "all"]:
        print(f"\n--- Running Metadata Filtered Search (Query: '{base_query_text}') ---")
        print(f"  Filtering for: import_pkg='{target_import_pkg}' AND symbol_name='{target_symbol_name}'")
        try:
            filtered_results = collection.query(
                query_embeddings=[query_embeddings],
                n_results=20, # Fetch up to 10 results that match the metadata filter
                where={
                    "import_pkg": {"$eq": target_import_pkg},
                    "symbol_name": {"$eq": target_symbol_name}
                },
                include=['documents', 'metadatas', 'distances']
            )

            if not filtered_results['documents'] or not filtered_results['documents'][0]:
                print("  No relevant snippets found with specified metadata filters.")
            else:
                for i, doc in enumerate(filtered_results['documents'][0]): # Use filtered_results here
                    distance = filtered_results['distances'][0][i]
                    metadata = filtered_results['metadatas'][0][i]
                    print(f"  Result {i+1} (Distance: {distance:.4f}):")
                    print(f"    File: {metadata.get('file_path', 'N/A')}")
                    print(f"    Import Pkg: {metadata.get('import_pkg', 'N/A')}")
                    print(f"    Symbol Name: {metadata.get('symbol_name', 'N/A')}")
                    print("    --- Code Snippet ---")
                    print(doc)
                    print("    --------------------")
        except Exception as e:
            print(f"  Error during metadata filtered query: {e}")

    # 3. Filtering by Document Content (Semantic + Document Contains Filter)
    if query_type in ["document", "all"]:
        print(f"\n--- Running Document Content Filtered Search (Query: '{base_query_text}') ---")
        print(f"  Filtering for: document content contains 'EndpointsHandler'")
        try:
            doc_content_results = collection.query(
                query_embeddings=[query_embeddings],
                n_results=20, # Fetch up to 10 results that match the document filter
                where_document={"$contains": "k8s.io/kubernetes/pkg/proxy/config.EndpointsHandler"},
                include=['documents', 'metadatas', 'distances']
            )

            if not doc_content_results['documents'] or not doc_content_results['documents'][0]:
                print("  No relevant snippets found with specified document content filters.")
            else:
                for i, doc in enumerate(doc_content_results['documents'][0]): # Use doc_content_results here
                    distance = doc_content_results['distances'][0][i]
                    metadata = doc_content_results['metadatas'][0][i]
                    print(f"  Result {i+1} (Distance: {distance:.4f}):")
                    print(f"    File: {metadata.get('file_path', 'N/A')}")
                    print(f"    Import Pkg: {metadata.get('import_pkg', 'N/A')}")
                    print(f"    Symbol Name: {metadata.get('symbol_name', 'N/A')}")
                    print("    --- Code Snippet ---")
                    print(doc)
                    print("    --------------------")
        except Exception as e:
            print(f"  Error during document content filtered query: {e}")

if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Run ChromaDB search queries for Go code snippets.")
    parser.add_argument(
        "--type",
        type=str,
        default="all",
        choices=["semantic", "metadata", "document", "all"],
        help="Specify the type of query to run: 'semantic', 'metadata', 'document', or 'all' (default)."
    )
    args = parser.parse_args()

    # To run this script:
    # 1. Make sure you have ChromaDB installed: `pip install chromadb sentence-transformers`
    # 2. Start a ChromaDB server in your terminal (e.g., `chroma run --host localhost --port 8080 --path ./my_chroma_db`)
    #    Replace `./my_chroma_db` with a path where you want ChromaDB to store its data.
    # 3. Run this Python script with an option:
    #    - `python your_script_name.py --type semantic`
    #    - `python your_script_name.py --type metadata` (Requires your data to have 'import_pkg' and 'symbol_name' metadata)
    #    - `python your_script_name.py --type document`
    #    - `python your_script_name.py` (Runs all types)
    #    - `python your_script_name.py --help` (To see options)

    run_chroma_search_query(args.type)
