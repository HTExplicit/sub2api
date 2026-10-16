import loginProtection from './loginProtection'
import overview from './overview'
import channels from './channels'
import accounts from './accounts'
import resources from './resources'
import ops from './ops'
import settings from './settings'
import audit from './audit'
import promptAudit from './promptAudit'
import systemPrompts from './systemPrompts'
import deepseekReasoning from './deepseekReasoning'
import pelicanTests from './pelicanTests'
import accountTasks from './accountTasks'
import plugins from './plugins'

export default {
  ...loginProtection,
  ...overview,
  ...channels,
  ...accounts,
  ...resources,
  ...ops,
  ...settings,
  ...audit,
  ...promptAudit,
  ...systemPrompts,
  ...deepseekReasoning,
  ...pelicanTests,
  ...accountTasks,
  ...plugins,
}
