import chromadb
import json # Import the json module for pretty-printing
import pprint # Import the pprint module (optional, but good for general data structures)

# Initialize ChromaDB HTTP client to connect to your local server
# Make sure your ChromaDB server is running at http://localhost:8080
try:
    client = chromadb.HttpClient(host="localhost", port=8080)
    print("Successfully connected to ChromaDB server at localhost:8080")
except Exception as e:
    print(f"Error connecting to ChromaDB server: {e}")
    print("Please ensure the server is running and accessible.")
    exit() # Exit if connection fails

# Get the collection by its name
collection_name = "go_code_chunks"
try:
    collection = client.get_collection(collection_name)
    print(f"Successfully retrieved collection: '{collection_name}'")
except Exception as e:
    print(f"Error retrieving collection '{collection_name}': {e}")
    print("Please ensure the collection exists on your ChromaDB server.")
    print("If it's a new server or you haven't added data, it might be empty or not yet created.")
    exit()

# Get all items from the collection
try:
    results = collection.get(
        ids=None,
        where=None,
        limit=None,
        offset=None,
        include=['documents','metadatas', 'embeddings']
    )

    # --- Start of the section that needs attention ---
    if results and results.get('ids') is not None and len(results['ids']) > 0:
        print(f"\n--- Chunks in Collection '{collection_name}' ({len(results['ids'])} total) ---")
        for i in range(len(results['ids'])):
            print(f"Chunk ID: {results['ids'][i]}")
            
            # Get the metadata for the current chunk
            #current_metadata = results['metadatas'][i]

            # --- Pretty-print the entire metadata dictionary ---
           # print("  Full Metadata (pretty-printed):")
           # print(json.dumps(current_metadata, indent=2))
            # OR using pprint:
            # pprint.pprint(current_metadata, indent=2)

            print(f"  Content:\n{results['documents'][i]}")
           # print(f"  Embeddings:\n{results['embeddings'][i]}")
            print("-" * 30)
    else:
        print(f"\nNo chunks found in the collection '{collection_name}'. Or results['ids'] is not as expected.")

except Exception as e:
    print(f"An error occurred while fetching chunks: {e}")
    print("This might happen if the collection is empty or if there's a problem with the server.")
