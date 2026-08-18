mod digest;
mod execute;
mod serialize;

use anyhow::Result;
use clap::Parser;
use clap::Subcommand;
use std::process::ExitCode;

#[derive(Parser)]
struct Args {
    #[command(subcommand)]
    command: DispatchCommand,
}

#[derive(Subcommand)]
enum DispatchCommand {
    Execute(execute::Args),
    Serialize(serialize::Args),
}

#[tokio::main]
async fn main() -> Result<ExitCode> {
    let args = Args::parse();
    match args.command {
        DispatchCommand::Execute(args) => execute::run(args).await,
        DispatchCommand::Serialize(args) => {
            serialize::run(args)?;
            Ok(ExitCode::SUCCESS)
        }
    }
}
