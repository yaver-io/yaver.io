"use strict";
const enabled=value=>["1","true","yes"].includes(String(value||"").trim().toLowerCase());
function provisionStudio(env=process.env){
 if(enabled(env.YAVER_IOT_EDGE)||enabled(env.YAVER_EDGE_LITE))return false;
 return enabled(env.YAVER_POSTINSTALL_STUDIO)||enabled(env.YAVER_CI)||enabled(env.YAVER_COMPLETE_AUTOMATION_HOST);
}
module.exports={provisionStudio};
