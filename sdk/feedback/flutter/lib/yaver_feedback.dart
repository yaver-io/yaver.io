/// Yaver Feedback SDK for Flutter.
///
/// Visual feedback collection for vibe coding workflows — shake-to-report,
/// screenshots, voice annotations, timeline-based feedback bundles, and P2P
/// device discovery.
///
/// ```dart
/// import 'package:yaver_feedback/yaver_feedback.dart';
///
/// void main() {
///   if (kDebugMode) {
///     YaverFeedback.init(FeedbackConfig(
///       agentUrl: 'http://192.168.1.100:18080',
///       authToken: 'your-token',
///       mode: FeedbackMode.narrated,
///       agentCommentaryLevel: 5,
///     ));
///   }
///   runApp(
///     MaterialApp(
///       builder: (context, child) => Stack(
///         children: [child!, const YaverFeedbackButton()],
///       ),
///       home: MyApp(),
///     ),
///   );
/// }
/// ```
library yaver_feedback;

export 'src/auth.dart';
export 'src/blackbox.dart';
export 'src/connection_widget.dart';
export 'src/device.dart';
export 'src/discovery.dart';
export 'src/feedback.dart';
export 'src/floating_button.dart';
export 'src/login_page.dart';
export 'src/p2p_client.dart';
export 'src/types.dart';
