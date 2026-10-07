package io.yaver.plainssh;

import android.app.Activity;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.graphics.Typeface;
import android.text.InputType;
import android.widget.*;
import org.json.*;
import java.nio.charset.StandardCharsets;
import java.util.concurrent.*;
import plainssh.Plainssh;

/** Native TV/Wear UI; no WebView requirement and no Yaver identity dependency.
 * Credentials stay in memory. Raw shows the actual tmux screen snapshot. */
public final class PlainSSHActivity extends Activity {
 private final ExecutorService work=Executors.newFixedThreadPool(3);
 private final Handler main=new Handler(Looper.getMainLooper());
 private LinearLayout body;private TextView status,screen;private EditText host,port,user,password,key,draft;
 private volatile String connection="";private volatile boolean alive=true;private boolean chat=false;private String lastInput="";
 private JSONObject target;private String pin="";
 interface Result {void done(Object result) throws Exception;}
 private JSONObject req(String op)throws Exception{return new JSONObject().put("op",op).put("id",connection);}
 private Object call(JSONObject request)throws Exception{JSONObject result=new JSONObject(Plainssh.invoke(request.toString()));if(!result.optBoolean("ok"))throw new Exception(result.optJSONObject("error").optString("message","SSH failed"));return result.opt("value");}
 private void run(JSONObject request,Result success){work.execute(()->{try{Object result=call(request);main.post(()->{if(alive)try{success.done(result);}catch(Exception e){status.setText(e.getMessage());}});}catch(Exception e){main.post(()->{if(alive)status.setText(e.getMessage());});}});}
 @Override public void onCreate(Bundle state){super.onCreate(state);form();}
 private void base(){ScrollView scroll=new ScrollView(this);body=new LinearLayout(this);body.setOrientation(LinearLayout.VERTICAL);body.setPadding(20,20,20,20);scroll.addView(body);setContentView(scroll);button("‹ Back",()->finish());status=new TextView(this);status.setText("Plain SSH · no Yaver account required");body.addView(status);}
 private EditText field(String name,boolean secret){EditText e=new EditText(this);e.setHint(name);e.setContentDescription(name);e.setInputType(InputType.TYPE_CLASS_TEXT|(secret?InputType.TYPE_TEXT_VARIATION_PASSWORD:InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS));body.addView(e);return e;}
 private void button(String title,Runnable action){Button b=new Button(this);b.setText(title);b.setOnClickListener(v->action.run());body.addView(b);}
 private void form(){base();host=field("Hostname or Tailscale address",false);port=field("SSH port",false);port.setText("22");user=field("SSH username",false);password=field("SSH password",true);key=field("SSH private key (optional)",true);
 button("Check host key",()->{try{target=new JSONObject().put("host",host.getText().toString().trim()).put("port",Integer.parseInt(port.getText().toString())).put("user",user.getText().toString().trim());JSONObject probe=new JSONObject(target.toString()).put("op","probe");run(probe,result->{pin=((JSONObject)result).getString("fingerprint");status.setText("Verify this key on the host:\n"+pin);button("Trust host and connect",this::connect);});}catch(Exception e){status.setText("Enter a valid SSH host, port and username.");}});
 }
 private void connect(){try{JSONObject request=new JSONObject(target.toString()).put("op","connect").put("fingerprint",pin).put("password",password.getText().toString()).put("privateKey",key.getText().toString());run(request,result->{connection=((JSONObject)result).getString("id");password.setText("");key.setText("");list();});}catch(Exception e){status.setText(e.getMessage());}}
 private void list()throws Exception{run(req("panes"),value->{base();JSONArray panes=(JSONArray)value;status.setText(panes.length()==0?"No tmux panes. Start tmux on this SSH account, then reconnect.":"Choose your existing pane");for(int i=0;i<panes.length();i++){JSONObject p=panes.getJSONObject(i);button(p.getString("session")+" · "+p.getString("id")+" · "+p.getString("command"),()->open(p));}});}
 private void open(JSONObject p){try{run(req("open").put("pane",p.getString("id")).put("session",p.getString("sessionId")).put("identity",p.getString("identity")),value->{base();status.setText("Live tmux screen · refreshes every second");button("Raw / Pane chat",()->{chat=!chat;});screen=new TextView(this);screen.setTypeface(Typeface.MONOSPACE);screen.setTextSize(12);body.addView(screen);draft=field("Message to selected pane",false);button("Send",()->send(draft.getText().toString(),"submit"));button("Enter",()->send("\r","write"));button("Esc",()->send("\u001b","write"));button("Ctrl-C",()->send("\u0003","write"));button("Sign this remote into Yaver",()->enroll(false));button("Install Yaver on remote",()->new android.app.AlertDialog.Builder(this).setMessage("Install yaver-cli using npm on this SSH host?").setPositiveButton("Install",(d,w)->enroll(true)).setNegativeButton("Cancel",null).show());button("Detach",()->finish());drain();snapshot();});}catch(Exception e){status.setText(e.getMessage());}}
 private void send(String text,String op){try{run(req(op).put("data",android.util.Base64.encodeToString(text.getBytes(StandardCharsets.UTF_8),android.util.Base64.NO_WRAP)),r->{if(op.equals("submit")){lastInput=text;draft.setText("");}});}catch(Exception e){status.setText(e.getMessage());}}
 private void drain(){final String id=connection;work.execute(()->{try{while(alive&&connection.equals(id))call(new JSONObject().put("op","read").put("id",id));}catch(Exception e){main.post(()->{if(alive)status.setText(e.getMessage());});}});}
 private void snapshot(){if(!alive)return;try{run(req("snapshot"),value->{screen.setText((chat&&!lastInput.isEmpty()?lastInput+"\n\nLive pane\n":"")+value.toString());main.postDelayed(this::snapshot,1000);});}catch(Exception e){status.setText(e.getMessage());}}
 private void enroll(boolean install){try{run(req(install?"installYaver":"enroll"),value->{status.setText("Remote Yaver setup running…");pollEnrollment();});}catch(Exception e){status.setText(e.getMessage());}}
 private void pollEnrollment(){if(!alive)return;try{run(req("enrollmentRead"),value->{JSONObject r=(JSONObject)value;status.append(r.optString("output"));if(!r.optBoolean("done"))main.postDelayed(this::pollEnrollment,750);else if(!r.optString("error").isEmpty())status.append("\n"+r.optString("error"));});}catch(Exception e){status.setText(e.getMessage());}}
 @Override public void onDestroy(){alive=false;main.removeCallbacksAndMessages(null);String id=connection;connection="";if(!id.isEmpty())work.execute(()->{try{call(new JSONObject().put("op","close").put("id",id));}catch(Exception ignored){}});work.shutdown();super.onDestroy();}
}
