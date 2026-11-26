class Api {
  static const base = 'https://api.example.com';

  static Future<Map<String, Object?>> post(String path, Map<String, Object?> body) async => {'url': '$base$path'};
  static Future<Map<String, Object?>> getChecked(String path) async => {'url': '$base$path'};
}
