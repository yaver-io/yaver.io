import 'package:flutter_test/flutter_test.dart';
import 'package:yaver_native_flutter_app/main.dart';

void main() {
  group('seedTodos', () {
    test('includes searchable manufacturing and quality-control terms', () {
      final todos = seedTodos();
      final allText = todos
          .expand((todo) => [todo.title, ...todo.keywords])
          .join(' ')
          .toLowerCase();

      expect(allText, contains('gkk'));
      expect(allText, contains('çkk'));
      expect(allText, contains('son kontrol formu'));
      expect(allText, contains('iş emri'));
      expect(allText, contains('üretim emri'));
    });
  });

  group('TodoItem.matches', () {
    const todo = TodoItem(
      title: 'Üretim emri planlama',
      keywords: ['uretim emri', 'üretim emri', 'imalat'],
    );

    test('matches title text', () {
      expect(todo.matches('planlama'), isTrue);
    });

    test('matches keyword text', () {
      expect(todo.matches('uretim emri'), isTrue);
    });

    test('rejects unrelated text', () {
      expect(todo.matches('sevkiyat'), isFalse);
    });
  });
}
