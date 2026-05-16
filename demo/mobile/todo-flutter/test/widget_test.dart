import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:yaver_native_flutter_app/main.dart';

void main() {
  testWidgets('renders seeded todo terms for remote-runtime smoke tests', (
    WidgetTester tester,
  ) async {
    await tester.pumpWidget(const YaverTodoApp());

    expect(find.text('Yaver Flutter Todo'), findsWidgets);
    expect(find.text('GKK kontrol listesi'), findsOneWidget);
    expect(find.text('ÇKK son kontrol formu'), findsOneWidget);
    expect(find.text('İş emri hazırlığı'), findsOneWidget);
    expect(find.text('Üretim emri planlama'), findsOneWidget);
  });

  testWidgets('filters seeded work items by keyword', (
    WidgetTester tester,
  ) async {
    await tester.pumpWidget(const YaverTodoApp());

    await tester.enterText(find.byKey(const Key('todo-search')), 'son kontrol');
    await tester.pump();

    expect(find.text('ÇKK son kontrol formu'), findsOneWidget);
    expect(find.text('GKK kontrol listesi'), findsNothing);
  });

  testWidgets('adds and completes a todo item', (WidgetTester tester) async {
    await tester.pumpWidget(const YaverTodoApp());

    await tester.enterText(find.byKey(const Key('new-todo-title')), 'Bakım emri');
    await tester.tap(find.byKey(const Key('add-todo')));
    await tester.pump();

    expect(find.text('Bakım emri'), findsOneWidget);
    expect(find.text('5 open'), findsOneWidget);

    await tester.tap(find.byKey(const Key('todo-Bakım emri')));
    await tester.pump();

    expect(find.text('4 open'), findsOneWidget);
  });
}
