import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:http/http.dart' as http;

void main() => runApp(const YaverServerlessTodoApp());

class Todo {
  const Todo({
    required this.id,
    required this.title,
    required this.done,
  });

  final String id;
  final String title;
  final bool done;

  factory Todo.fromJson(Map<String, dynamic> json) {
    final rawDone = json['done'];
    return Todo(
      id: '${json['id'] ?? ''}',
      title: '${json['title'] ?? json['text'] ?? ''}',
      done: rawDone == true || rawDone == 1 || rawDone == '1' || rawDone == 'true',
    );
  }
}

class YaverTodoApi {
  YaverTodoApi({required this.baseUrl, required this.slug, required this.token});

  final String baseUrl;
  final String slug;
  final String token;

  Uri _uri(String path) => Uri.parse('${baseUrl.replaceAll(RegExp(r'/+$'), '')}/data/$slug$path');

  Map<String, String> get _headers => {
        'Accept': 'application/json',
        if (token.isNotEmpty) 'Authorization': 'Bearer $token',
      };

  Future<List<Todo>> list() async {
    final response = await http.get(_uri('/todos?limit=100'), headers: _headers);
    final body = _decode(response);
    final rows = body['rows'] is List ? body['rows'] as List : const [];
    return rows.whereType<Map>().map((row) => Todo.fromJson(Map<String, dynamic>.from(row))).toList();
  }

  Future<void> create(String title) async {
    final trimmed = title.trim();
    if (trimmed.isEmpty) return;
    final response = await http.post(
      _uri('/todos'),
      headers: {..._headers, 'Content-Type': 'application/json'},
      body: jsonEncode({
        'id': 'todo-${DateTime.now().microsecondsSinceEpoch}',
        'title': trimmed,
        'done': false,
        'owner_id': 'alice',
      }),
    );
    _decode(response);
  }

  Future<void> setDone(String id, bool done) async {
    final response = await http.patch(
      _uri('/todos/${Uri.encodeComponent(id)}'),
      headers: {..._headers, 'Content-Type': 'application/json'},
      body: jsonEncode({'done': done}),
    );
    _decode(response);
  }

  Future<void> delete(String id) async {
    final response = await http.delete(_uri('/todos/${Uri.encodeComponent(id)}'), headers: _headers);
    _decode(response);
  }

  Map<String, dynamic> _decode(http.Response response) {
    final decoded = response.body.isEmpty ? <String, dynamic>{} : jsonDecode(response.body) as Map<String, dynamic>;
    if (response.statusCode < 200 || response.statusCode >= 300) {
      throw Exception(decoded['error'] ?? decoded['message'] ?? 'Yaver Serverless request failed');
    }
    return decoded;
  }
}

class YaverServerlessTodoApp extends StatelessWidget {
  const YaverServerlessTodoApp({super.key});

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'Yaver Serverless Todo',
      debugShowCheckedModeBanner: false,
      theme: ThemeData(
        useMaterial3: true,
        colorSchemeSeed: const Color(0xff0f8b8d),
        scaffoldBackgroundColor: const Color(0xfff7f8fb),
      ),
      home: const TodoHomePage(),
    );
  }
}

class TodoHomePage extends StatefulWidget {
  const TodoHomePage({super.key});

  @override
  State<TodoHomePage> createState() => _TodoHomePageState();
}

class _TodoHomePageState extends State<TodoHomePage> {
  final _baseUrl = TextEditingController(text: 'http://127.0.0.1:18080');
  final _slug = TextEditingController(text: 'yaver-serverless-todo');
  final _token = TextEditingController();
  final _draft = TextEditingController();
  List<Todo> _todos = const [];
  String _error = '';
  bool _loading = false;

  YaverTodoApi get _api => YaverTodoApi(baseUrl: _baseUrl.text, slug: _slug.text, token: _token.text);

  @override
  void initState() {
    super.initState();
    _refresh();
  }

  @override
  void dispose() {
    _baseUrl.dispose();
    _slug.dispose();
    _token.dispose();
    _draft.dispose();
    super.dispose();
  }

  Future<void> _refresh() async {
    setState(() {
      _loading = true;
      _error = '';
    });
    try {
      final todos = await _api.list();
      setState(() => _todos = todos);
    } catch (error) {
      setState(() => _error = '$error');
    } finally {
      setState(() => _loading = false);
    }
  }

  Future<void> _add() async {
    final title = _draft.text;
    _draft.clear();
    await _api.create(title).then((_) => _refresh()).catchError((error) => setState(() => _error = '$error'));
  }

  @override
  Widget build(BuildContext context) {
    final openCount = _todos.where((todo) => !todo.done).length;
    return Scaffold(
      appBar: AppBar(
        title: const Text('Yaver Serverless Todo'),
        actions: [IconButton(onPressed: _refresh, icon: const Icon(Icons.refresh))],
      ),
      body: SafeArea(
        child: ListView(
          padding: const EdgeInsets.all(18),
          children: [
            Text(_loading ? 'Syncing...' : '$openCount open tasks', style: Theme.of(context).textTheme.bodyMedium),
            const SizedBox(height: 12),
            TextField(controller: _baseUrl, decoration: const InputDecoration(labelText: 'Yaver Serverless URL', border: OutlineInputBorder())),
            const SizedBox(height: 8),
            TextField(controller: _slug, decoration: const InputDecoration(labelText: 'Project slug', border: OutlineInputBorder())),
            const SizedBox(height: 8),
            TextField(controller: _token, obscureText: true, decoration: const InputDecoration(labelText: 'Project token', border: OutlineInputBorder())),
            if (_error.isNotEmpty) Padding(padding: const EdgeInsets.only(top: 10), child: Text(_error, style: const TextStyle(color: Color(0xffc2410c)))),
            const SizedBox(height: 14),
            Row(
              children: [
                Expanded(child: TextField(controller: _draft, decoration: const InputDecoration(labelText: 'What needs doing?', border: OutlineInputBorder()), onSubmitted: (_) => _add())),
                const SizedBox(width: 10),
                IconButton.filled(onPressed: _add, icon: const Icon(Icons.add)),
              ],
            ),
            const SizedBox(height: 14),
            if (_todos.isEmpty) const Center(child: Padding(padding: EdgeInsets.all(24), child: Text('No serverless todos yet.'))),
            for (final todo in _todos)
              Padding(
                padding: const EdgeInsets.only(bottom: 8),
                child: ListTile(
                  shape: RoundedRectangleBorder(side: const BorderSide(color: Color(0xffd9e0ea)), borderRadius: BorderRadius.circular(8)),
                  tileColor: Colors.white,
                  leading: Checkbox(value: todo.done, onChanged: (value) => _api.setDone(todo.id, value ?? false).then((_) => _refresh())),
                  title: Text(todo.title, style: TextStyle(decoration: todo.done ? TextDecoration.lineThrough : null)),
                  trailing: IconButton(icon: const Icon(Icons.delete_outline), onPressed: () => _api.delete(todo.id).then((_) => _refresh())),
                ),
              ),
          ],
        ),
      ),
    );
  }
}
