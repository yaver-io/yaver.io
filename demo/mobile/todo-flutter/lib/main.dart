// Yaver Flutter todo fixture.
// Used by the remote-runtime WebRTC path to verify Flutter apps run as
// native emulator/simulator processes rather than Hermes guest bundles.

import 'package:flutter/material.dart';

void main() => runApp(const YaverTodoApp());

class TodoItem {
  const TodoItem({
    required this.title,
    required this.keywords,
    this.done = false,
  });

  final String title;
  final List<String> keywords;
  final bool done;

  TodoItem copyWith({String? title, List<String>? keywords, bool? done}) {
    return TodoItem(
      title: title ?? this.title,
      keywords: keywords ?? this.keywords,
      done: done ?? this.done,
    );
  }

  bool matches(String query) {
    final normalized = query.trim().toLowerCase();
    if (normalized.isEmpty) return true;
    final haystack = <String>[title, ...keywords].join(' ').toLowerCase();
    return haystack.contains(normalized);
  }
}

List<TodoItem> seedTodos() {
  return const [
    TodoItem(
      title: 'GKK kontrol listesi',
      keywords: ['gkk', 'giris kalite kontrol', 'kalite', 'malzeme kabul'],
    ),
    TodoItem(
      title: 'ÇKK son kontrol formu',
      keywords: ['çkk', 'cikis kalite kontrol', 'son kontrol formu', 'sevkiyat'],
    ),
    TodoItem(
      title: 'İş emri hazırlığı',
      keywords: ['is emri', 'iş emri', 'operasyon', 'bakim'],
    ),
    TodoItem(
      title: 'Üretim emri planlama',
      keywords: ['uretim emri', 'üretim emri', 'imalat', 'hat planlama'],
    ),
  ];
}

class YaverTodoApp extends StatelessWidget {
  const YaverTodoApp({super.key});

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'Yaver Flutter Todo',
      debugShowCheckedModeBanner: false,
      theme: ThemeData(
        useMaterial3: true,
        colorSchemeSeed: const Color(0xff2563eb),
        scaffoldBackgroundColor: const Color(0xfff8fafc),
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
  final _searchController = TextEditingController();
  final _newTodoController = TextEditingController();
  late List<TodoItem> _todos;
  String _query = '';

  @override
  void initState() {
    super.initState();
    _todos = seedTodos();
    _searchController.addListener(() {
      setState(() => _query = _searchController.text);
    });
  }

  @override
  void dispose() {
    _searchController.dispose();
    _newTodoController.dispose();
    super.dispose();
  }

  List<TodoItem> get _visibleTodos {
    return _todos.where((todo) => todo.matches(_query)).toList();
  }

  int get _openCount => _todos.where((todo) => !todo.done).length;

  void _addTodo() {
    final title = _newTodoController.text.trim();
    if (title.isEmpty) return;
    setState(() {
      _todos = [
        TodoItem(title: title, keywords: title.split(RegExp(r'\s+'))),
        ..._todos,
      ];
      _newTodoController.clear();
    });
  }

  void _toggleTodo(int index, bool? value) {
    setState(() {
      _todos[index] = _todos[index].copyWith(done: value ?? false);
    });
  }

  @override
  Widget build(BuildContext context) {
    final visible = _visibleTodos;
    return Scaffold(
      appBar: AppBar(
        title: const Text('Yaver Flutter Todo'),
        centerTitle: false,
      ),
      body: SafeArea(
        child: ListView(
          padding: const EdgeInsets.all(20),
          children: [
            _SummaryCard(openCount: _openCount, totalCount: _todos.length),
            const SizedBox(height: 16),
            TextField(
              key: const Key('todo-search'),
              controller: _searchController,
              decoration: const InputDecoration(
                labelText: 'Search work items',
                hintText: 'GKK, ÇKK, iş emri, üretim emri',
                prefixIcon: Icon(Icons.search),
                border: OutlineInputBorder(),
              ),
            ),
            const SizedBox(height: 16),
            Row(
              children: [
                Expanded(
                  child: TextField(
                    key: const Key('new-todo-title'),
                    controller: _newTodoController,
                    decoration: const InputDecoration(
                      labelText: 'New work item',
                      border: OutlineInputBorder(),
                    ),
                    onSubmitted: (_) => _addTodo(),
                  ),
                ),
                const SizedBox(width: 12),
                IconButton.filled(
                  key: const Key('add-todo'),
                  tooltip: 'Add work item',
                  onPressed: _addTodo,
                  icon: const Icon(Icons.add),
                ),
              ],
            ),
            const SizedBox(height: 16),
            if (visible.isEmpty)
              const Padding(
                padding: EdgeInsets.symmetric(vertical: 32),
                child: Center(child: Text('No matching work items')),
              )
            else
              ...visible.map((todo) {
                final originalIndex = _todos.indexOf(todo);
                return Padding(
                  padding: const EdgeInsets.only(bottom: 10),
                  child: _TodoTile(
                    todo: todo,
                    onChanged: (value) => _toggleTodo(originalIndex, value),
                  ),
                );
              }),
          ],
        ),
      ),
    );
  }
}

class _SummaryCard extends StatelessWidget {
  const _SummaryCard({required this.openCount, required this.totalCount});

  final int openCount;
  final int totalCount;

  @override
  Widget build(BuildContext context) {
    return DecoratedBox(
      decoration: BoxDecoration(
        color: Colors.white,
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: const Color(0xffdbe3ef)),
      ),
      child: Padding(
        padding: const EdgeInsets.all(18),
        child: Row(
          children: [
            const Icon(Icons.fact_check_outlined, size: 32),
            const SizedBox(width: 12),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    '$openCount open',
                    style: Theme.of(context).textTheme.titleLarge,
                  ),
                  Text('$totalCount work items ready for WebRTC testing'),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _TodoTile extends StatelessWidget {
  const _TodoTile({required this.todo, required this.onChanged});

  final TodoItem todo;
  final ValueChanged<bool?> onChanged;

  @override
  Widget build(BuildContext context) {
    return DecoratedBox(
      decoration: BoxDecoration(
        color: Colors.white,
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: const Color(0xffe2e8f0)),
      ),
      child: CheckboxListTile(
        key: Key('todo-${todo.title}'),
        value: todo.done,
        onChanged: onChanged,
        title: Text(
          todo.title,
          style: TextStyle(
            decoration: todo.done ? TextDecoration.lineThrough : null,
          ),
        ),
        subtitle: Text(todo.keywords.join(' · ')),
        controlAffinity: ListTileControlAffinity.leading,
      ),
    );
  }
}
