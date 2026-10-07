Pod::Spec.new do |s|
  s.name = 'YaverPlainSSH'
  s.version = '0.1.0'
  s.summary = 'Account-independent SSH and exact tmux pane transport'
  s.homepage = 'https://github.com/yaver-io/yaver.io'
  s.license = { :type => 'FSL-1.1-Apache-2.0' }
  s.author = 'Yaver'
  s.source = { :git => 'https://github.com/yaver-io/yaver.io.git' }
  s.platform = :ios, '15.1'
  s.source_files = 'ios/*.{h,m}'
  s.vendored_frameworks = 'build/PlainSSH.xcframework'
  s.dependency 'React-Core'
end
