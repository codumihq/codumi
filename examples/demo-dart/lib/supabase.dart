// Stand-ins for supabase_flutter so the demo needs no packages.
class SupabaseQueryBuilder {
  SupabaseQueryBuilder select([String columns = '*']) => this;
  SupabaseQueryBuilder insert(Map<String, Object?> row) => this;
  SupabaseQueryBuilder update(Map<String, Object?> row) => this;
  SupabaseQueryBuilder eq(String column, Object value) => this;
}

class StorageFileApi {
  Future<String> upload(String path, List<int> bytes) async => path;
}

class SupabaseStorageClient {
  StorageFileApi from(String bucket) => StorageFileApi();
}

class SupabaseClient {
  final storage = SupabaseStorageClient();
  SupabaseQueryBuilder from(String table) => SupabaseQueryBuilder();
  Future<Object?> rpc(String fn, {Map<String, Object?>? params}) async => null;
}
