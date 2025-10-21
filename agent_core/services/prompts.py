"""Prompt templates used for diagnostics."""

DIAGNOSE_PROMPT = """I have an error with my application, can you check the logs for the
{service} service{namespace_instruction}, I only want you to check the pods logs, look up only the 1000
most recent logs. Feel free to scroll up until you find relevant errors that
contain reference to a file.

Once you have these errors and the file name, get the file contents of the path
{project_root} for the repository
{repo_name} in the organisation
{organisation}. Keep listing the directories until you find the file name and then get
the contents of the file.

Please use the file contents to diagnose the error, then please create an issue in
GitHub reporting a fix for the issue. Once you have diagnosed the error and created an
issue please report this to the following Slack channel: {slack_channel_id}.

Please only do this ONCE, don't keep making issues or sending messages to Slack."""
