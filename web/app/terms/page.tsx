import Link from "next/link";
import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "Terms of Service - Yaver",
};

export default function TermsOfServicePage() {
  return (
    <div className="mx-auto max-w-3xl px-6 py-16 md:py-24">
      <div className="mb-8">
        <Link href="/" className="text-sm text-surface-500 hover:text-surface-300">
          &larr; Back to Home
        </Link>
      </div>

      <h1 className="mb-2 text-3xl font-bold text-surface-50">Terms of Service</h1>
      <p className="mb-12 text-sm text-surface-500">Effective Date: March 12, 2026</p>

      <div className="prose-legal space-y-6 text-sm leading-relaxed text-surface-400">
        <p>
          Please read these Terms of Service (&ldquo;Terms&rdquo; or &ldquo;Agreement&rdquo;)
          carefully. This is a legal agreement between you (&ldquo;you,&rdquo; &ldquo;your,&rdquo;
          or &ldquo;End-User&rdquo;) and SIMKAB ELEKTRIK (&ldquo;we,&rdquo; &ldquo;us,&rdquo; or
          &ldquo;Developer&rdquo;) concerning your use of the Yaver application, desktop agent,
          CLI tool, and related services (the &ldquo;Service&rdquo;), made available through
          Apple&apos;s App Store, Google Play Store, and our website at yaver.io.
        </p>
        <p>
          By downloading, installing, or using the Service, you agree to be bound by this
          Agreement. If you do not agree, do not download, install, or use the Service.
        </p>

        <h2 className="!mt-10 border-b border-surface-800 pb-2 text-lg font-semibold text-surface-100">
          1. Acknowledgement
        </h2>
        <p>
          1.1 This Agreement is concluded between you and SIMKAB ELEKTRIK only, and not with
          Apple Inc. (&ldquo;Apple&rdquo;) or Google LLC (&ldquo;Google&rdquo;). Neither Apple
          nor Google is a party to this Agreement and has no obligations with respect to the
          Service or its content.
        </p>
        <p>
          1.2 This Agreement does not conflict with the Apple Media Services Terms and Conditions
          or the Google Play Developer Distribution Agreement.
        </p>

        <h2 className="!mt-10 border-b border-surface-800 pb-2 text-lg font-semibold text-surface-100">
          2. Scope of License
        </h2>
        <p>
          2.1 Subject to your compliance with this Agreement, SIMKAB ELEKTRIK grants you a
          limited, non-exclusive, non-transferable license to download, install, and use the
          Service on devices you own or control, solely for your personal or internal business
          use in software development and related activities.
        </p>
        <p>
          2.2 You may not rent, lease, lend, sell, redistribute, sublicense, reverse-engineer,
          or create derivative works based on the Service.
        </p>

        <h2 className="!mt-10 border-b border-surface-800 pb-2 text-lg font-semibold text-surface-100">
          3. Description of Service
        </h2>
        <p>
          3.1 Yaver is a peer-to-peer development tool that enables you to interact with Claude
          AI from any device by connecting directly to your development machines. The Service
          consists of:
        </p>
        <ul className="list-disc space-y-2 pl-6">
          <li>A mobile application (iOS and Android) for sending tasks and viewing output</li>
          <li>A desktop agent (macOS, Windows, Linux) that runs Claude CLI on your machine</li>
          <li>A CLI tool for terminal-based interaction</li>
          <li>A web application for authentication and account management</li>
          <li>A backend service for authentication and peer device discovery only</li>
        </ul>
        <p>
          3.2 The Service uses a peer-to-peer architecture. Your code, prompts, and AI outputs
          are transmitted directly between your devices and are never stored on or transmitted
          through our servers.
        </p>

        <h2 className="!mt-10 border-b border-surface-800 pb-2 text-lg font-semibold text-surface-100">
          4. Permitted Use
        </h2>
        <p>
          4.1 You agree to use the Service only for lawful purposes and in accordance with these
          Terms. You must not use the Service in any way that is unlawful, fraudulent, or harmful.
        </p>
        <p>
          4.2 You are solely responsible for ensuring that your use of Claude AI through the
          Service complies with Anthropic&apos;s usage policies and any applicable laws and
          regulations.
        </p>
        <p>
          4.3 You are responsible for maintaining the security of your devices and network
          connections used with the Service.
        </p>

        <h2 className="!mt-10 border-b border-surface-800 pb-2 text-lg font-semibold text-surface-100">
          5. Third-Party Services
        </h2>
        <p>
          5.1 The Service integrates with third-party services including Anthropic&apos;s Claude
          AI and authentication providers (Apple, Google, Microsoft). You agree to comply with
          the applicable terms of those third-party services.
        </p>
        <p>
          5.2 We are not responsible for the availability, accuracy, or content of third-party
          services. Your use of Claude AI is subject to Anthropic&apos;s terms of service.
        </p>

        <h2 className="!mt-10 border-b border-surface-800 pb-2 text-lg font-semibold text-surface-100">
          6. Account and Security
        </h2>
        <p>
          6.1 You are responsible for safeguarding your account credentials and for any activity
          that occurs under your account.
        </p>
        <p>
          6.2 You must notify us immediately of any unauthorized use of your account or any
          other breach of security.
        </p>
        <p>
          6.3 You may delete your account at any time through the mobile app or by contacting
          us. Account deletion will permanently remove your data from our servers.
        </p>

        <h2 className="!mt-10 border-b border-surface-800 pb-2 text-lg font-semibold text-surface-100">
          7. Maintenance and Support
        </h2>
        <p>
          7.1 SIMKAB ELEKTRIK is solely responsible for providing maintenance and support for
          the Service as described in this Agreement or as required by applicable law.
        </p>
        <p>
          7.2 Apple and Google have no obligation to furnish any maintenance or support services
          with respect to the Service.
        </p>

        <h2 className="!mt-10 border-b border-surface-800 pb-2 text-lg font-semibold text-surface-100">
          8. Warranty Disclaimer
        </h2>
        <p>
          8.1 TO THE MAXIMUM EXTENT PERMITTED BY APPLICABLE LAW, THE SERVICE IS PROVIDED
          &ldquo;AS IS&rdquo; AND &ldquo;AS AVAILABLE&rdquo; WITHOUT WARRANTY OF ANY KIND,
          EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO WARRANTIES OF MERCHANTABILITY,
          FITNESS FOR A PARTICULAR PURPOSE, OR NON-INFRINGEMENT.
        </p>
        <p>
          8.2 We do not warrant that the Service will be uninterrupted, error-free, or secure.
          We do not guarantee the accuracy or reliability of any AI-generated output.
        </p>
        <p>
          8.3 If the Service fails to conform to any applicable warranty, you may notify Apple
          or Google, who may refund the purchase price (if any). To the extent permitted by law,
          Apple and Google have no other warranty obligation with respect to the Service.
        </p>

        <h2 className="!mt-10 border-b border-surface-800 pb-2 text-lg font-semibold text-surface-100">
          9. Limitation of Liability
        </h2>
        <p>
          9.1 TO THE MAXIMUM EXTENT PERMITTED BY LAW, IN NO EVENT SHALL SIMKAB ELECTRIC BE
          LIABLE FOR ANY INDIRECT, INCIDENTAL, SPECIAL, CONSEQUENTIAL, OR PUNITIVE DAMAGES,
          OR ANY LOSS OF PROFITS OR REVENUES, WHETHER INCURRED DIRECTLY OR INDIRECTLY, OR
          ANY LOSS OF DATA, USE, GOODWILL, OR OTHER INTANGIBLE LOSSES.
        </p>
        <p>
          9.2 You acknowledge that AI-generated outputs may contain errors or inaccuracies.
          You are solely responsible for reviewing and validating any code or content generated
          through the Service before using it in production.
        </p>

        <h2 className="!mt-10 border-b border-surface-800 pb-2 text-lg font-semibold text-surface-100">
          10. Product Claims
        </h2>
        <p>
          10.1 SIMKAB ELEKTRIK, not Apple or Google, is responsible for addressing any claims
          you or any third party may have relating to: (a) product liability; (b) failure to
          conform to applicable legal or regulatory requirements; and (c) claims arising under
          consumer protection or similar legislation.
        </p>

        <h2 className="!mt-10 border-b border-surface-800 pb-2 text-lg font-semibold text-surface-100">
          11. Intellectual Property
        </h2>
        <p>
          11.1 The Service and all intellectual property rights therein are and shall remain the
          sole and exclusive property of SIMKAB ELEKTRIK. No rights are granted to you other
          than the limited license expressly set forth in this Agreement.
        </p>
        <p>
          11.2 In the event of any third-party claim that the Service or your use thereof
          infringes a third party&apos;s intellectual property rights, SIMKAB ELEKTRIK (not
          Apple or Google) will be solely responsible for the investigation, defense, settlement,
          and discharge of any such claim.
        </p>

        <h2 className="!mt-10 border-b border-surface-800 pb-2 text-lg font-semibold text-surface-100">
          12. Third-Party Beneficiary
        </h2>
        <p>
          12.1 You and SIMKAB ELEKTRIK acknowledge that Apple and Google are third-party
          beneficiaries of this Agreement and, upon your acceptance, will have the right to
          enforce this Agreement against you.
        </p>

        <h2 className="!mt-10 border-b border-surface-800 pb-2 text-lg font-semibold text-surface-100">
          13. Termination
        </h2>
        <p>
          13.1 This Agreement is effective until terminated. Your rights under this Agreement
          will terminate automatically if you fail to comply with any of its terms. Upon
          termination, you must cease all use of the Service and delete all copies.
        </p>

        <h2 className="!mt-10 border-b border-surface-800 pb-2 text-lg font-semibold text-surface-100">
          14. Changes to This Agreement
        </h2>
        <p>
          14.1 We may modify this Agreement at any time. Changes are effective upon posting or
          notification through the Service. Your continued use constitutes acceptance of the
          updated terms.
        </p>

        <h2 className="!mt-10 border-b border-surface-800 pb-2 text-lg font-semibold text-surface-100">
          15. Governing Law
        </h2>
        <p>
          15.1 This Agreement shall be governed by and construed in accordance with the laws
          of Turkey, without regard to conflict of law principles.
        </p>

        <h2 className="!mt-10 border-b border-surface-800 pb-2 text-lg font-semibold text-surface-100">
          16. Contact
        </h2>
        <p>SIMKAB ELEKTRIK</p>
        <p>Yunus Emre Mah. Adalar Sokak No:12 Sancaktepe / Istanbul, Turkey</p>
        <p>Email: <a href="mailto:kivanc.cakmak@simkab.com" className="text-indigo-400 hover:text-indigo-700 dark:hover:text-indigo-300">kivanc.cakmak@simkab.com</a></p>

        <div className="!mt-16 border-t border-surface-800 pt-6 text-xs text-surface-600">
          <p>SIMKAB ELEKTRIK &mdash; Yunus Emre Mah. Adalar Sokak No:12, Sancaktepe, Istanbul, Turkey</p>
          <p className="mt-1">Copyright &copy; {new Date().getFullYear()} Yaver. All Rights Reserved.</p>
        </div>
      </div>
    </div>
  );
}
