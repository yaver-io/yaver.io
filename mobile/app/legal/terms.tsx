import { router } from "expo-router";
import React from "react";
import { Pressable, ScrollView, StyleSheet, Text, View } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { useColors } from "../../src/context/ThemeContext";
import { AppBackButton } from "../../src/components/AppBackButton";

export default function TermsOfServiceScreen() {
  const c = useColors();

  const sections = [
    {
      title: "1. Acknowledgement",
      content: [
        '1.1 This Agreement is concluded between you and SIMKAB ELEKTRIK only, and not with Apple Inc. ("Apple") or Google LLC ("Google"). Neither Apple nor Google is a party to this Agreement and has no obligations with respect to the Service or its content.',
        "1.2 This Agreement does not conflict with the Apple Media Services Terms and Conditions or the Google Play Developer Distribution Agreement.",
      ],
    },
    {
      title: "2. Scope of License",
      content: [
        "2.1 Subject to your compliance with this Agreement, SIMKAB ELEKTRIK grants you a limited, non-exclusive, non-transferable license to download, install, and use the Service on devices you own or control, solely for your personal or internal business use in software development and related activities.",
        "2.2 You may not rent, lease, lend, sell, redistribute, sublicense, reverse-engineer, or create derivative works based on the Service.",
      ],
    },
    {
      title: "3. Description of Service",
      content: [
        "3.1 Yaver is a peer-to-peer development tool that enables you to interact with Claude AI from any device by connecting directly to your development machines. The Service consists of:",
      ],
      bullets: [
        "A mobile application (iOS and Android) for sending tasks and viewing output",
        "A desktop agent (macOS, Windows, Linux) that runs Claude CLI on your machine",
        "A CLI tool for terminal-based interaction",
        "A web application for authentication and account management",
        "A backend service for authentication and peer device discovery only",
      ],
      after: [
        "3.2 The Service uses a peer-to-peer architecture. Your code, prompts, and AI outputs are transmitted directly between your devices and are never stored on or transmitted through our servers.",
      ],
    },
    {
      title: "4. Permitted Use",
      content: [
        "4.1 You agree to use the Service only for lawful purposes and in accordance with these Terms. You must not use the Service in any way that is unlawful, fraudulent, or harmful.",
        "4.2 You are solely responsible for ensuring that your use of Claude AI through the Service complies with Anthropic's usage policies and any applicable laws and regulations.",
        "4.3 You are responsible for maintaining the security of your devices and network connections used with the Service.",
      ],
    },
    {
      title: "5. Third-Party Services",
      content: [
        "5.1 The Service integrates with third-party services including Anthropic's Claude AI and authentication providers (Apple, Google, Microsoft). You agree to comply with the applicable terms of those third-party services.",
        "5.2 We are not responsible for the availability, accuracy, or content of third-party services. Your use of Claude AI is subject to Anthropic's terms of service.",
      ],
    },
    {
      title: "6. Account and Security",
      content: [
        "6.1 You are responsible for safeguarding your account credentials and for any activity that occurs under your account.",
        "6.2 You must notify us immediately of any unauthorized use of your account or any other breach of security.",
        "6.3 You may delete your account at any time through the mobile app or by contacting us. Account deletion will permanently remove your data from our servers.",
      ],
    },
    {
      title: "7. Maintenance and Support",
      content: [
        "7.1 SIMKAB ELEKTRIK is solely responsible for providing maintenance and support for the Service as described in this Agreement or as required by applicable law.",
        "7.2 Apple and Google have no obligation to furnish any maintenance or support services with respect to the Service.",
      ],
    },
    {
      title: "8. Warranty Disclaimer",
      content: [
        '8.1 TO THE MAXIMUM EXTENT PERMITTED BY APPLICABLE LAW, THE SERVICE IS PROVIDED "AS IS" AND "AS AVAILABLE" WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE, OR NON-INFRINGEMENT.',
        "8.2 We do not warrant that the Service will be uninterrupted, error-free, or secure. We do not guarantee the accuracy or reliability of any AI-generated output.",
        "8.3 If the Service fails to conform to any applicable warranty, you may notify Apple or Google, who may refund the purchase price (if any). To the extent permitted by law, Apple and Google have no other warranty obligation with respect to the Service.",
      ],
    },
    {
      title: "9. Limitation of Liability",
      content: [
        "9.1 TO THE MAXIMUM EXTENT PERMITTED BY LAW, IN NO EVENT SHALL SIMKAB ELEKTRIK BE LIABLE FOR ANY INDIRECT, INCIDENTAL, SPECIAL, CONSEQUENTIAL, OR PUNITIVE DAMAGES, OR ANY LOSS OF PROFITS OR REVENUES, WHETHER INCURRED DIRECTLY OR INDIRECTLY, OR ANY LOSS OF DATA, USE, GOODWILL, OR OTHER INTANGIBLE LOSSES.",
        "9.2 You acknowledge that AI-generated outputs may contain errors or inaccuracies. You are solely responsible for reviewing and validating any code or content generated through the Service before using it in production.",
      ],
    },
    {
      title: "10. Product Claims",
      content: [
        "10.1 SIMKAB ELEKTRIK, not Apple or Google, is responsible for addressing any claims you or any third party may have relating to: (a) product liability; (b) failure to conform to applicable legal or regulatory requirements; and (c) claims arising under consumer protection or similar legislation.",
      ],
    },
    {
      title: "11. Intellectual Property",
      content: [
        "11.1 The Service and all intellectual property rights therein are and shall remain the sole and exclusive property of SIMKAB ELEKTRIK. No rights are granted to you other than the limited license expressly set forth in this Agreement.",
        "11.2 In the event of any third-party claim that the Service or your use thereof infringes a third party's intellectual property rights, SIMKAB ELEKTRIK (not Apple or Google) will be solely responsible for the investigation, defense, settlement, and discharge of any such claim.",
      ],
    },
    {
      title: "12. Third-Party Beneficiary",
      content: [
        "12.1 You and SIMKAB ELEKTRIK acknowledge that Apple and Google are third-party beneficiaries of this Agreement and, upon your acceptance, will have the right to enforce this Agreement against you.",
      ],
    },
    {
      title: "13. Termination",
      content: [
        "13.1 This Agreement is effective until terminated. Your rights under this Agreement will terminate automatically if you fail to comply with any of its terms. Upon termination, you must cease all use of the Service and delete all copies.",
      ],
    },
    {
      title: "14. Changes to This Agreement",
      content: [
        "14.1 We may modify this Agreement at any time. Changes are effective upon posting or notification through the Service. Your continued use constitutes acceptance of the updated terms.",
      ],
    },
    {
      title: "15. Governing Law",
      content: [
        "15.1 This Agreement shall be governed by and construed in accordance with the laws of Turkey, without regard to conflict of law principles.",
      ],
    },
    {
      title: "16. Contact",
      content: [
        "SIMKAB ELEKTRIK",
        "Yunus Emre Mah. Adalar Sokak No:12 Sancaktepe / Istanbul, Turkey",
        "Email: kivanc.cakmak@simkab.com",
      ],
    },
  ];

  return (
    <SafeAreaView style={[styles.safeArea, { backgroundColor: c.bg }]}>
      <View style={[styles.header, { borderBottomColor: c.border }]}>
        <AppBackButton onPress={() => router.back()} textStyle={styles.backButton} />
        <Text style={[styles.headerTitle, { color: c.textPrimary }]}>Terms of Service</Text>
        <View style={styles.headerSpacer} />
      </View>
      <ScrollView style={styles.container} contentContainerStyle={styles.content}>
        <Text style={[styles.lastUpdated, { color: c.textMuted }]}>Effective Date: March 12, 2026</Text>

        <Text style={[styles.body, { color: c.textSecondary }]}>
          Please read these Terms of Service ("Terms" or "Agreement") carefully. This is a legal agreement between you ("you," "your," or "End-User") and SIMKAB ELEKTRIK ("we," "us," or "Developer") concerning your use of the Yaver application, desktop agent, CLI tool, and related services (the "Service"), made available through Apple's App Store, Google Play Store, and our website at yaver.io.
        </Text>
        <Text style={[styles.body, { color: c.textSecondary }]}>
          By downloading, installing, or using the Service, you agree to be bound by this Agreement. If you do not agree, do not download, install, or use the Service.
        </Text>

        {sections.map((section) => (
          <View key={section.title}>
            <Text style={[styles.sectionTitle, { color: c.textPrimary, borderBottomColor: c.border }]}>
              {section.title}
            </Text>
            {section.content.map((text, i) => (
              <Text key={i} style={[styles.body, { color: c.textSecondary }]}>{text}</Text>
            ))}
            {section.bullets?.map((bullet, i) => (
              <Text key={i} style={[styles.bullet, { color: c.textSecondary }]}>{"\u2022 "}{bullet}</Text>
            ))}
            {section.after?.map((text, i) => (
              <Text key={i} style={[styles.body, { color: c.textSecondary }]}>{text}</Text>
            ))}
          </View>
        ))}

        <View style={[styles.footer, { borderTopColor: c.border }]}>
          <Text style={[styles.footerText, { color: c.textMuted }]}>
            SIMKAB ELEKTRIK {"\u2014"} Yunus Emre Mah. Adalar Sokak No:12, Sancaktepe, Istanbul, Turkey
          </Text>
          <Text style={[styles.footerText, { color: c.textMuted }]}>
            Copyright {"\u00A9"} {new Date().getFullYear()} Yaver. All Rights Reserved.
          </Text>
        </View>
      </ScrollView>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  safeArea: { flex: 1 },
  header: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    paddingHorizontal: 16,
    paddingVertical: 12,
    borderBottomWidth: 1,
  },
  backButton: { fontSize: 16 },
  headerTitle: { fontSize: 17, fontWeight: "600" },
  headerSpacer: { width: 40 },
  container: { flex: 1 },
  content: { padding: 16, paddingBottom: 40 },
  lastUpdated: { fontSize: 12, marginBottom: 16 },
  sectionTitle: {
    fontSize: 16,
    fontWeight: "700",
    marginTop: 28,
    marginBottom: 12,
    paddingBottom: 8,
    borderBottomWidth: 1,
  },
  body: {
    fontSize: 13,
    lineHeight: 20,
    marginBottom: 8,
  },
  bullet: {
    fontSize: 13,
    lineHeight: 20,
    marginBottom: 4,
    paddingLeft: 8,
  },
  footer: {
    marginTop: 32,
    paddingTop: 16,
    borderTopWidth: 1,
  },
  footerText: { fontSize: 11, marginBottom: 4 },
});
