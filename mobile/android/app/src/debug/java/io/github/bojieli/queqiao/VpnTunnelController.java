package io.github.bojieli.queqiao;

import android.annotation.SuppressLint;
import android.content.Intent;
import android.net.VpnService;
import android.provider.Settings;
import android.view.View;
import android.widget.Button;
import android.widget.LinearLayout;
import android.widget.RadioButton;
import android.widget.RadioGroup;
import android.widget.TextView;

/**
 * Drives the full-device tunnel. Android asks for VPN consent once per app, the
 * traffic policy decides which destinations the interface claims, and the
 * system VPN settings screen is the only place a user can revoke it.
 */
final class VpnTunnelController implements TunnelController {
    private final TunnelHost host;

    VpnTunnelController(TunnelHost host) {
        this.host = host;
    }

    @Override
    public String modeId() {
        return QueqiaoVpnService.MODE;
    }

    @Override
    public String title() {
        return "Full device tunnel";
    }

    @Override
    public String summary() {
        return "Queqiao installs a VPN interface and carries every app's traffic.";
    }

    @Override
    public String noun() {
        return "tunnel";
    }

    @Override
    public Intent consentIntent() {
        return VpnService.prepare(host.activity());
    }

    @Override
    public void connect(String profileId) {
        TunnelBroadcast.connect(host.activity(), QueqiaoVpnService.class, profileId);
    }

    @Override
    public void disconnect() {
        TunnelBroadcast.disconnect(host.activity(), QueqiaoVpnService.class);
    }

    @Override
    public void requestStatus() {
        TunnelBroadcast.requestStatus(host.activity(), QueqiaoVpnService.class);
    }

    @Override
    public void renderConnectionDetails(
            UiKit ui, LinearLayout card, ProfileRepository.ProfileRecord profile) {
        ui.addLabelValue(card, "Traffic policy", profile.trafficPolicy.title);
    }

    @Override
    @SuppressLint("SetTextI18n")
    public void renderProfileOptions(
            UiKit ui,
            LinearLayout content,
            ProfileRepository.ProfileRecord profile,
            boolean editable) {
        TextView policyTitle = ui.sectionTitle("Traffic policy");
        policyTitle.setPadding(0, ui.dp(18), 0, ui.dp(2));
        content.addView(policyTitle, UiKit.matchWrap());
        RadioGroup policies = new RadioGroup(host.activity());
        for (TrafficPolicy policy : TrafficPolicy.values()) {
            RadioButton option = new RadioButton(host.activity());
            option.setId(View.generateViewId());
            option.setText(policy.title + "\n" + policy.detail);
            option.setTextSize(14);
            option.setTag(policy);
            option.setChecked(profile.trafficPolicy == policy);
            option.setEnabled(editable);
            policies.addView(option, UiKit.matchWrap());
        }
        policies.setOnCheckedChangeListener((group, checkedId) -> {
            RadioButton option = group.findViewById(checkedId);
            if (option != null && option.getTag() instanceof TrafficPolicy) {
                updateTrafficPolicy(profile.id, (TrafficPolicy) option.getTag());
            }
        });
        content.addView(policies, UiKit.matchWrap());
    }

    @Override
    public void renderSettings(UiKit ui, LinearLayout content) {
        LinearLayout card = ui.card();
        card.addView(ui.sectionTitle("VPN interface"), UiKit.matchWrap());
        ui.addBodyText(
                card,
                "Android grants VPN consent to one app at a time. Revoke it from the system settings screen.");
        Button systemSettings = ui.secondaryButton("Open Android VPN settings");
        systemSettings.setOnClickListener(view -> openVpnSettings());
        card.addView(systemSettings, ui.topSpaced());
        content.addView(card, ui.spacedCard());
    }

    private void openVpnSettings() {
        try {
            host.activity().startActivity(new Intent(Settings.ACTION_VPN_SETTINGS));
        } catch (Exception exception) {
            host.failure("VPN settings are unavailable", exception);
        }
    }

    private void updateTrafficPolicy(String profileId, TrafficPolicy policy) {
        if (host.connectionActive()) {
            host.failure(
                    "Disconnect first",
                    new IllegalStateException("Disconnect before changing the traffic policy"));
            return;
        }
        host.background(() -> {
            try {
                host.repository().setTrafficPolicy(profileId, policy);
                host.activity().runOnUiThread(host::refresh);
            } catch (Exception exception) {
                host.failure("Could not update traffic policy", exception);
            }
        });
    }
}
