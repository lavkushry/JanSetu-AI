fn main() -> Result<(), Box<dyn std::error::Error>> {
    std::env::set_var("PROTOC", protoc_bin_vendored::protoc_bin_path()?);
    tonic_prost_build::compile_protos(
        "../../contracts/proto/recommendation/v1/recommendation.proto",
    )?;
    println!("cargo:rerun-if-changed=../../contracts/proto/recommendation/v1/recommendation.proto");
    Ok(())
}
