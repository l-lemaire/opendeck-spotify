// Property inspector for the Spotify plugin. Registers with OpenDeck, shows
// the connection status, runs the web API login through the plugin (which
// asks OpenDeck to open the browser), and saves display options for the
// play/pause key.

"use strict";

let websocket = null;
let actionInfo = null;
let settings = {};

const $ = (id) => document.getElementById(id);

function connectOpenActionSocket(port, uuid, registerEvent, info, inActionInfo) {
	actionInfo = typeof inActionInfo === "string" ? JSON.parse(inActionInfo) : inActionInfo;
	settings = (actionInfo.payload && actionInfo.payload.settings) || {};

	websocket = new WebSocket("ws://127.0.0.1:" + port);
	websocket.onopen = () => {
		websocket.send(JSON.stringify({ event: registerEvent, uuid: uuid }));
		sendToPlugin({ event: "status" });
	};
	websocket.onmessage = (msg) => {
		const data = JSON.parse(msg.data);
		if (data.event === "sendToPropertyInspector") onPluginMessage(data.payload || {});
		if (data.event === "didReceiveSettings") settings = (data.payload && data.payload.settings) || {};
	};
	websocket.onclose = () => setStatus("local-status", "Disconnected from OpenDeck", "error");

	const isPlayPause = (actionInfo.action || "").endsWith(".play-pause");
	$("display").hidden = !isPlayPause;
	if (isPlayPause) {
		for (const id of ["show_art", "show_title", "show_artist", "show_time", "show_progress"]) {
			$(id).checked = settings[id] !== false;
			$(id).addEventListener("change", saveDisplay);
		}
		$("text_scale").value = String(settings.text_scale || 1);
		$("text_scale").addEventListener("change", saveDisplay);
	}
	$("login").addEventListener("click", () => {
		$("login").disabled = true;
		setStatus("login-status", "Starting…", "busy");
		sendToPlugin({ event: "login", client_id: $("client_id").value.trim() });
	});
	$("logout").addEventListener("click", () => sendToPlugin({ event: "logout" }));
	$("refresh").addEventListener("click", () => sendToPlugin({ event: "status" }));
}
const connectElgatoStreamDeckSocket = connectOpenActionSocket;

function sendToPlugin(payload) {
	websocket.send(JSON.stringify({ event: "sendToPlugin", action: actionInfo.action, context: actionInfo.context, payload: payload }));
}

function saveDisplay() {
	for (const id of ["show_art", "show_title", "show_artist", "show_time", "show_progress"]) {
		settings[id] = $(id).checked;
	}
	settings.text_scale = parseFloat($("text_scale").value) || 1;
	websocket.send(JSON.stringify({ event: "setSettings", context: actionInfo.context, payload: settings }));
}

function onPluginMessage(payload) {
	switch (payload.event) {
		case "status":
			setStatus("local-status", payload.local ? "Spotify client running" : "Spotify client not running", payload.local ? "ok" : "error");
			if (payload.web_api) {
				setStatus("api-status", "Connected", "ok");
				$("logout").hidden = false;
				$("login").textContent = "Reconnect";
			} else {
				setStatus("api-status", "Not connected", payload.client_id ? "" : "");
				$("logout").hidden = true;
				$("login").textContent = "Connect Spotify";
			}
			if (payload.client_id && !$("client_id").value) $("client_id").value = payload.client_id;
			let now = "Nothing playing";
			if (payload.source === "local") now = "Controlling this computer" + (payload.track ? ": " + payload.track : "");
			if (payload.source === "remote") now = "Controlling " + (payload.device || "another device") + (payload.track ? ": " + payload.track : "");
			setStatus("now-status", now, payload.source === "none" ? "" : "ok");
			$("login").disabled = false;
			break;
		case "login":
			setStatus("login-status", payload.message, payload.stage === "error" ? "error" : payload.stage === "done" ? "ok" : "busy");
			if (payload.stage === "error" || payload.stage === "done") $("login").disabled = false;
			break;
	}
}

function setStatus(id, text, state) {
	const el = $(id);
	el.textContent = text;
	el.className = "status" + (state ? " " + state : "");
}
