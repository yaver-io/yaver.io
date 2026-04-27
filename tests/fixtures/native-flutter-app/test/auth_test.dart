// Unit tests for the Flutter fixture's authentication helper.
// Run with: flutter test (auto-skipped by yaver tests when flutter is missing).

import 'package:flutter_test/flutter_test.dart';
import 'package:yaver_native_flutter_app/main.dart';

void main() {
  group('authenticate', () {
    test('accepts valid hardcoded credentials', () {
      expect(authenticate('admin', 'admin'), isTrue);
    });

    test('rejects wrong password', () {
      expect(authenticate('admin', 'wrong'), isFalse);
    });

    test('rejects unknown user', () {
      expect(authenticate('intruder', 'admin'), isFalse);
    });

    test('rejects empty inputs', () {
      expect(authenticate('', ''), isFalse);
    });
  });
}
